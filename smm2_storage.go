package main

// SMM2 level storage — the object-storage half of DataStore.
//
// A course is two layers: (1) NEX DataStore RMC metadata (title, tags, stats — the
// search/browse layer), and (2) the level BLOB itself, transferred over plain HTTP(S)
// to a URL the server hands back from prepare_post_object / prepare_get_object. Nintendo
// returns presigned AWS-S3 / CloudFront URLs; we run our OWN object store and hand back
// URLs into it, so uploaded courses live on the Nextendo VPS instead of Nintendo's S3.
//
// Flow:
//   prepare_post_object(24)  -> allocate data_id, return {data_id, url=/object/<id>} ; stash pending meta
//   console PUTs the blob     -> stored at <dataDir>/<id>.bin
//   complete_post_object(26) -> mark the course ready in the catalog (persisted)
//   prepare_get_object(25)   -> return {url=/object/<id>, size} for the requested data_id
//   console GETs the blob     -> served from <dataDir>/<id>.bin
//
// The exact upload handshake the console performs (PUT vs multipart POST, required
// headers) is not in the measured — no level was uploaded during it — so the object
// endpoint accepts PUT and POST and logs what actually arrives, to verify on the first
// real upload (measured > guess).

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	storagePort = envOrInt("STORAGE_PORT", 60078)
	// storageURL is the PUBLIC base the console dials for blob transfer. Locally it is
	// the game host itself; on the server set STORAGE_URL to the routed https origin.
	storageURL = envOr("STORAGE_URL", fmt.Sprintf("https://%s:%d", nextendoHost, storagePort))
	// storageHostPort is the scheme-less host:port the console POSTs uploads to (the
	// measured S3 responses carry a scheme-less host and the console prepends https://).
	storageHostPort = envOr("STORAGE_HOSTPORT", fmt.Sprintf("%s:%d", nextendoHost, storagePort))
	storageDir = envOr("STORAGE_DIR", "smm2_objects")
	// storageCAFile, if set, is returned as root_ca_cert so a real console trusts our
	// object server's TLS cert. Empty = rely on the console's default trust (emulator).
	storageCAFile = os.Getenv("STORAGE_CA_FILE")
)

// courseMeta is the catalog entry for one uploaded course.
type courseMeta struct {
	DataID    uint64   `json:"data_id"`
	OwnerPID  uint64   `json:"owner_pid"`
	Name      string   `json:"name"`
	DataType  uint16   `json:"data_type"`
	MetaHex   string   `json:"meta_hex"` // course header (SMM2 meta_binary), hex
	Tags      []string `json:"tags"`
	Size      uint32   `json:"size"`
	Ready     bool     `json:"ready"` // set by complete_post_object
	CreatedAt int64    `json:"created_at"`
}

type courseStore struct {
	mu      sync.Mutex
	nextID  uint64
	byID    map[uint64]*courseMeta
	catalog string // JSON path
	rootCA  []byte
}

var courses = &courseStore{byID: map[uint64]*courseMeta{}, nextID: 1000}

// loadStore restores the catalog + data_id counter from disk.
func (c *courseStore) load() {
	_ = os.MkdirAll(storageDir, 0o755)
	c.catalog = filepath.Join(storageDir, "catalog.json")
	if b, err := os.ReadFile(c.catalog); err == nil {
		var saved struct {
			NextID  uint64        `json:"next_id"`
			Courses []*courseMeta `json:"courses"`
		}
		if json.Unmarshal(b, &saved) == nil {
			if saved.NextID > c.nextID {
				c.nextID = saved.NextID
			}
			for _, m := range saved.Courses {
				c.byID[m.DataID] = m
			}
		}
	}
	if storageCAFile != "" {
		if b, err := os.ReadFile(storageCAFile); err == nil {
			c.rootCA = b
		}
	}
	fmt.Printf("[SMM2 Storage] catalogue chargé: %d cours, nextID=%d, dir=%s, url=%s\n",
		len(c.byID), c.nextID, storageDir, storageURL)
}

// persist writes the catalog back to disk (called under lock).
func (c *courseStore) persistLocked() {
	out := struct {
		NextID  uint64        `json:"next_id"`
		Courses []*courseMeta `json:"courses"`
	}{NextID: c.nextID}
	for _, m := range c.byID {
		out.Courses = append(out.Courses, m)
	}
	if b, err := json.MarshalIndent(out, "", "  "); err == nil {
		tmp := c.catalog + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, c.catalog)
		}
	}
}

// alloc reserves a new data_id and stashes the pending metadata.
func (c *courseStore) alloc(ownerPID uint64, name string, dataType uint16, metaBin []byte, tags []string, size uint32) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	c.byID[id] = &courseMeta{
		DataID: id, OwnerPID: ownerPID, Name: name, DataType: dataType,
		MetaHex: fmt.Sprintf("%x", metaBin), Tags: tags, Size: size,
		CreatedAt: nowUnix(),
	}
	c.persistLocked()
	return id
}

// complete marks a course ready (or drops it if the upload was cancelled).
func (c *courseStore) complete(dataID uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byID[dataID]
	if m == nil {
		return
	}
	if ok {
		m.Ready = true
	} else {
		delete(c.byID, dataID)
	}
	c.persistLocked()
}

func (c *courseStore) get(dataID uint64) *courseMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byID[dataID]
}

// setSize records the byte size once a blob PUT completes.
func (c *courseStore) setSize(dataID uint64, size uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.byID[dataID]; m != nil {
		m.Size = size
		c.persistLocked()
	}
}

func blobPath(dataID uint64) string { return filepath.Join(storageDir, strconv.FormatUint(dataID, 10)+".bin") }

// startStorageServer serves blob PUT/POST/GET over HTTPS on storagePort.
func startStorageServer() {
	courses.load()
	mux := http.NewServeMux()
	mux.HandleFunc("/object/", objectHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	// S3-style presigned-POST upload: SMM2 uploads a course (method 66 = level data,
	// 132 = thumbnails) as a multipart/form-data POST carrying a `key` + `file`, exactly
	// like AWS S3 browser uploads. We rewrite the bucket host to ourselves and accept it.
	mux.HandleFunc("/", s3PostHandler)
	// Log EVERY inbound request (method, path, content-type, length) before dispatch,
	// so an upload that never reaches a handler still shows what the console attempted.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[SMM2 Storage] <- %s %s ct=%q len=%s from %s\n",
			r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Content-Length"), r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", storagePort), Handler: logged}
	fmt.Printf("[SMM2 Storage] listening HTTPS :%d (blob store)\n", storagePort)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		fmt.Printf("[SMM2 Storage] stopped: %v\n", err)
	}
}

// objectHandler stores (PUT/POST) and serves (GET) course blobs by data_id.
func objectHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/object/")
	if i := strings.IndexAny(idStr, "/?"); i >= 0 {
		idStr = idStr[:i]
	}
	dataID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad data_id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		body, _ := readAllLimited(r, 64<<20) // courses are small; cap at 64 MiB
		if err := os.WriteFile(blobPath(dataID), body, 0o644); err != nil {
			fmt.Printf("[SMM2 Storage] PUT %d FAILED: %v\n", dataID, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		courses.setSize(dataID, uint32(len(body)))
		fmt.Printf("[SMM2 Storage] %s /object/%d <- %d bytes (ct=%q)\n", r.Method, dataID, len(body), r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		b, err := os.ReadFile(blobPath(dataID))
		if err != nil {
			fmt.Printf("[SMM2 Storage] GET %d -> 404\n", dataID)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		fmt.Printf("[SMM2 Storage] GET /object/%d -> %d bytes\n", dataID, len(b))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// s3PostHandler accepts the console's S3-style multipart upload (an alias for what
// Nintendo routes to AWS S3). It reads the `key` (the object path, e.g.
// ".../data/00059850236-00001") and the `file` part, stores the blob under the key,
// and answers 204 like S3. Signature/policy fields are ignored — we own the bucket.
func s3PostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(96 << 20); err != nil {
		fmt.Printf("[SMM2 Storage] POST %s: pas multipart (%v) — ct=%q\n", r.URL.Path, err, r.Header.Get("Content-Type"))
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	key := r.FormValue("key")
	// The console's `file` part carries no filename, so Go's parser files it under
	// MultipartForm.Value, not .File — read from whichever holds it.
	var blob []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		blob, _ = io.ReadAll(io.LimitReader(f, 96<<20))
	} else if vals := r.MultipartForm.Value["file"]; len(vals) > 0 {
		blob = []byte(vals[0])
	} else {
		fmt.Printf("[SMM2 Storage] POST key=%q aucun champ 'file' — champs=%v\n", key, formFieldNames(r))
		http.Error(w, "no file", http.StatusBadRequest)
		return
	}
	name := sanitizeKey(key)
	if name == "" {
		http.Error(w, "no key", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(filepath.Join(storageDir, name), blob, 0o644); err != nil {
		fmt.Printf("[SMM2 Storage] UPLOAD key=%q STORE FAIL: %v\n", key, err)
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	// Mirror S3's POST success headers: the console reads the ETag (the object's MD5)
	// to confirm/link the upload; a bare 204 with no ETag can stall the next step.
	sum := md5.Sum(blob)
	w.Header().Set("ETag", fmt.Sprintf("%q", hex.EncodeToString(sum[:])))
	w.Header().Set("Server", "AmazonS3")
	w.Header().Set("x-amz-request-id", "NEXTENDO0000000000")
	fmt.Printf("[SMM2 Storage] UPLOAD OK key=%q -> %s (%d bytes, etag=%x)\n", key, name, len(blob), sum[:4])
	w.WriteHeader(http.StatusNoContent) // 204, like S3
}

// sanitizeKey turns an S3 object key into a safe flat filename.
func sanitizeKey(key string) string {
	if key == "" {
		return ""
	}
	repl := strings.NewReplacer("/", "_", ":", "_", "\\", "_", "?", "_", "..", "_")
	return "obj_" + repl.Replace(key)
}

// formFieldNames lists the multipart field names present (for diagnosing an upload).
func formFieldNames(r *http.Request) []string {
	var out []string
	if r.MultipartForm != nil {
		for k := range r.MultipartForm.Value {
			out = append(out, k)
		}
		for k := range r.MultipartForm.File {
			out = append(out, k+"(file)")
		}
	}
	return out
}

func readAllLimited(r *http.Request, max int64) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 1<<16)
	tmp := make([]byte, 32<<10)
	var total int64
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > max {
				return buf, fmt.Errorf("too large")
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, nil
		}
	}
}

// nowUnix returns the current unix time (isolated so the rest of the file has no
// direct time import churn).
func nowUnix() int64 { return time.Now().Unix() }
