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
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	nex "github.com/NextendoNetwork/nextendo-nex"
	"io"
	"mime"
	"mime/multipart"
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
	storageDir      = envOr("STORAGE_DIR", "smm2_objects")
	// storageCAFile, if set, is returned as root_ca_cert so a real console trusts our
	// object server's TLS cert. Empty = rely on the console's default trust (emulator).
	storageCAFile = os.Getenv("STORAGE_CA_FILE")
)

// courseMeta is the catalog entry for one uploaded course.
type courseMeta struct {
	DataID   uint64 `json:"data_id"`
	OwnerPID uint64 `json:"owner_pid"`
	Name     string `json:"name"`
	// Description : saisie par le joueur a la publication. Elle arrive dans le parametre
	// de la methode 66 et doit revenir dans CourseInfo, sinon la fiche du niveau est
	// muette.
	Description string   `json:"description,omitempty"`
	DataType    uint16   `json:"data_type"`
	MetaHex     string   `json:"meta_hex"` // course header (SMM2 meta_binary), hex
	Tags        []string `json:"tags"`

	// Champs lus dans l'en-tete CHIFFRE du niveau (voir smm2_bcd.go). Ils etaient
	// ecrits a zero dans CourseInfo, ce qui annoncait tous les niveaux en SMB1.
	// EnteteLue distingue « pas encore lu » de « lu et vaut zero » — sans ce drapeau on
	// re-dechiffrerait 376 Ko a chaque demarrage pour un niveau en SMB1 theme 0.
	EnteteLue   bool   `json:"entete_lue,omitempty"`
	Style       uint8  `json:"style,omitempty"`
	Theme       uint8  `json:"theme,omitempty"`
	Minuterie   uint16 `json:"minuterie,omitempty"`
	Condition   uint32 `json:"condition,omitempty"`
	CondCat     uint8  `json:"cond_cat,omitempty"`
	CondAmpleur uint16 `json:"cond_ampleur,omitempty"`
	// Essais qu'il a fallu a l'auteur pour terminer son propre niveau : la seule mesure
	// de difficulte disponible avant que d'autres joueurs y jouent.
	EssaisAuteur uint32 `json:"essais_auteur,omitempty"`
	// Temps mis par l'auteur pour terminer son niveau, en millisecondes. C'est le champ
	// « Time of uploader » de CourseTimeStats, que j'ecrivais a zero en disant qu'il
	// restait a trouver d'ou il sortait. Il sortait de la : du fichier du niveau.
	TempsAuteur uint32 `json:"temps_auteur,omitempty"`
	Size        uint32 `json:"size"`
	Ready       bool   `json:"ready"` // set by complete_post_object
	CreatedAt   int64  `json:"created_at"`
}

type courseStore struct {
	// RWMutex et non Mutex : les lectures dominent largement (chaque consultation de
	// profil, chaque fiche de niveau) et n'ont aucune raison de s'exclure entre elles.
	mu     sync.RWMutex
	nextID uint64
	byID   map[uint64]*courseMeta
	// publiesParPID : combien de niveaux publies par joueur. Tenu a jour au lieu d'etre
	// recompte : il alimente UserInfo, donc chaque affichage de profil le consultait.
	publiesParPID map[uint64]uint32
	catalog       string // JSON path
	rootCA        []byte
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
	c.recompterPubliesLocked()
	fmt.Printf("[SMM2 Storage] catalogue chargé: %d cours, nextID=%d, dir=%s, url=%s\n",
		len(c.byID), c.nextID, storageDir, storageURL)
}

// persist writes the catalog back to disk (called under lock).
func (c *courseStore) persistLocked() {
	// Le catalogue vient de changer : le compte par joueur avec lui. C'est le seul
	// endroit par ou passent toutes les modifications, donc le seul a devoir y penser.
	c.recompterPubliesLocked()

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
	// L'en-tete se lit juste apres, quand le niveau est marque pret : c'est le premier
	// moment ou le fichier est complet sur le disque.
	defer func() {
		if ok {
			go c.lireEnteteSi(dataID)
		}
	}()
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

// setTags remplace les deux etiquettes d'un niveau et persiste immediatement.
//
// Les etiquettes sont stockees en chaines dans le catalogue, alors que le protocole les
// transmet en Uint8 : on convertit, sans chercher a rendre leur libelle. tagOuZero fait
// le trajet inverse a l'ecriture de CourseInfo.
func (c *courseStore) setTags(dataID uint64, tag1, tag2 uint8) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.byID[dataID]; m != nil {
		m.Tags = []string{strconv.FormatUint(uint64(tag1), 10), strconv.FormatUint(uint64(tag2), 10)}
		c.persistLocked()
	}
}

// lireEnteteSi lit l'en-tete d'un niveau et le range dans le catalogue, une seule fois.
//
// Un echec est journalise mais N'EMPECHE RIEN : un niveau dont on ne sait pas lire
// l'en-tete reste jouable, il s'affichera simplement avec les valeurs par defaut. Faire
// echouer la publication pour cela serait echanger un affichage faux contre un niveau
// perdu, et ce serait un plus mauvais marche.
func (c *courseStore) lireEnteteSi(dataID uint64) {
	c.mu.Lock()
	m := c.byID[dataID]
	if m == nil || !m.Ready || estFichierRattache(m.Name) || (m.EnteteLue && m.EssaisAuteur > 0) {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Le dechiffrement se fait HORS du verrou : 376 Ko d'AES par niveau, et au demarrage
	// il y en a autant que de niveaux publies. Tenir le verrou pendant ce temps
	// bloquerait toutes les requetes des joueurs deja connectes.
	e, err := enteteDuBlob(dataID)

	c.mu.Lock()
	defer c.mu.Unlock()
	m = c.byID[dataID]
	if m == nil {
		return
	}
	m.EnteteLue = true
	if err != nil {
		fmt.Printf("[SMM2 BCD] data_id=%d en-tete illisible : %v\n", dataID, err)
		c.persistLocked()
		return
	}
	m.Style, m.Theme = e.Style, e.Theme
	m.Minuterie, m.Condition = e.Minuterie, e.Condition
	m.CondCat, m.CondAmpleur = e.CondCat, e.CondAmpleur
	m.EssaisAuteur, m.TempsAuteur = e.EssaisAuteur, e.TempsAuteur
	styles := [...]string{"SMB1", "SMB3", "SMW", "NSMBU", "SM3DW"}
	fmt.Printf("[SMM2 BCD] data_id=%d %q -> style=%s theme=%d minuterie=%d essais_auteur=%d temps_auteur=%dms\n",
		dataID, e.Nom, styles[e.Style], e.Theme, e.Minuterie, e.EssaisAuteur, e.TempsAuteur)
	c.persistLocked()
}

// lireEntetesManquants rattrape les niveaux publies avant que cette lecture existe.
func (c *courseStore) lireEntetesManquants() {
	c.mu.Lock()
	var aFaire []uint64
	for id, m := range c.byID {
		// EssaisAuteur == 0 signale une fiche lue par une version anterieure du code,
		// avant que ce champ existe : l'auteur doit terminer son niveau pour le publier,
		// donc un vrai zero est impossible. On la relit.
		if m.Ready && (!m.EnteteLue || m.EssaisAuteur == 0) && !estFichierRattache(m.Name) {
			aFaire = append(aFaire, id)
		}
	}
	c.mu.Unlock()
	if len(aFaire) == 0 {
		return
	}
	fmt.Printf("[SMM2 BCD] %d niveau(x) sans en-tete lu, rattrapage\n", len(aFaire))
	for _, id := range aFaire {
		c.lireEnteteSi(id)
	}
}

// nombrePublies compte les niveaux publies par un joueur.
//
// On ecarte les fichiers rattaches — miniatures et rediffusions vivent dans le meme
// catalogue sous un nom « rel-… ». Les compter multiplierait le total par cinq, ce qui
// est precisement le genre de chiffre qui a l'air plausible et remplit le quota du
// joueur sans qu'il comprenne pourquoi.
func (c *courseStore) nombrePublies(pid uint64) uint32 {
	// VERROU DE LECTURE, pas exclusif. La premiere version prenait Lock() : elle
	// serialisait chaque consultation de profil contre les publications en cours, alors
	// qu'elle ne fait que lire. Et GetUsers est la methode la plus appelee du serveur.
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.publiesParPID[pid]
}

// recompterPublies etablit le compte par joueur en UNE passe. Appelee au chargement et
// apres chaque changement du catalogue — pas a chaque consultation de profil.
func (c *courseStore) recompterPubliesLocked() {
	m := make(map[uint64]uint32, len(c.publiesParPID))
	for _, cm := range c.byID {
		if cm.Ready && !estFichierRattache(cm.Name) {
			m[cm.OwnerPID]++
		}
	}
	c.publiesParPID = m
}

// oublier retire un objet du catalogue. Le fichier est efface par l'appelant.
func (c *courseStore) oublier(dataID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, dataID)
	c.persistLocked()
}

func blobPath(dataID uint64) string {
	return filepath.Join(storageDir, strconv.FormatUint(dataID, 10)+".bin")
}

// startStorageServer serves blob PUT/POST/GET over HTTPS on storagePort.
func startStorageServer() {
	courses.load()
	resultats.charger(storageDir)
	commentaires.charger(storageDir)
	endless.charger(storageDir)
	// Une seule passe au demarrage pour etablir premieres reussites et records, et pour
	// rattraper les fichiers ecrits avant que ces compteurs existent.
	resultats.reconstruireCompteurs()
	go menageInitial()
	// En tache de fond : le dechiffrement des niveaux deja publies ne doit pas retarder
	// l'ouverture des ports, sinon personne ne peut se connecter pendant ce temps.
	go courses.lireEntetesManquants()

	// On fournit a la bibliotheque le comptage des niveaux publies : c'est lui qui
	// alimente l'ecran « niveaux publies » du joueur (UserInfo.unk9).
	nex.SMM2CompteurPublies = courses.nombrePublies
	nex.SMM2CompteurStats = resultats.statsDe
	nex.SMM2MasqueBooleensFn = func() uint32 { return uint32(formeEssai(9998, 7)) }
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

		// La console televerse en multipart/form-data, meme sur cette route. Sans ce
		// depouillement on ecrivait l'ENVELOPPE MIME entiere dans le blob : les cinq
		// objets du premier niveau publie commencaient tous par « ----------BOUNDA ».
		// Le televersement reussissait, le catalogue affichait la bonne taille, et le
		// telechargement rendait fidelement 376 971 octets — dont les premiers etaient
		// des en-tetes MIME. Le jeu descendait le niveau en entier puis le rejetait,
		// ce qui se presentait a l'ecran comme une « erreur de connexion » et non
		// comme un fichier corrompu. C'est pour cela que la panne a resiste : chaque
		// etape se declarait en succes.
		//
		// s3PostHandler fait deja ce travail pour la route S3 ; on l'applique ici.
		if partie, ok := extraireFichierMultipart(r.Header.Get("Content-Type"), body); ok {
			fmt.Printf("[SMM2 Storage] %s /object/%d : multipart depouille, %d -> %d octets\n",
				r.Method, dataID, len(body), len(partie))
			body = partie
		}

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

// extraireFichierMultipart rend le contenu du fichier televerse quand le corps est un
// multipart/form-data, et dit franchement s'il ne l'etait pas.
//
// On accepte n'importe quelle partie porteuse de donnees plutot que d'exiger le nom
// « file » : la console n'etiquette pas toujours ses parties de la meme facon, et une
// exigence trop stricte nous ferait retomber, en silence, sur l'enveloppe brute — soit
// exactement le defaut que cette fonction corrige. On retient donc la partie la plus
// volumineuse, qui est la charge utile.
func extraireFichierMultipart(contentType string, corps []byte) ([]byte, bool) {
	if contentType == "" || len(corps) == 0 {
		return nil, false
	}
	mediatype, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediatype, "multipart/") {
		return nil, false
	}
	frontiere := params["boundary"]
	if frontiere == "" {
		return nil, false
	}

	lecteur := multipart.NewReader(bytes.NewReader(corps), frontiere)
	var meilleure []byte
	for {
		partie, err := lecteur.NextPart()
		if err != nil {
			break // io.EOF, ou un corps tronque : on garde ce qu'on a deja lu
		}
		donnees, errLire := io.ReadAll(io.LimitReader(partie, 96<<20))
		partie.Close()
		if errLire == nil && len(donnees) > len(meilleure) {
			meilleure = donnees
		}
	}
	if len(meilleure) == 0 {
		return nil, false
	}
	return meilleure, true
}
