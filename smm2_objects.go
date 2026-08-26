package main

// DataStore object-transfer methods (prepare_post 24 / prepare_get 25 / complete_post
// 26) backed by our own object store (smm2_storage.go) instead of Nintendo's presigned
// S3/CloudFront. These are what actually upload/download a course's level BLOB.

import (
	"bytes"
	"fmt"
	"strings"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// SMM2's real upload flow is NOT prepare_post_object(24) — it is a set of custom
// methods (66 = level data, 132 = thumbnails) that hand back an AWS-S3 presigned-POST
// descriptor (url + policy/signature form fields). The console then does a multipart
// POST of the blob to that bucket. We keep the measured descriptor verbatim but swap
// the bucket host for our own object store, so the blob is POSTed to us instead.
//
// The upload structures are custom, nested and undocumented, so rather than decode and
// rebuild them we replace the S3 host STRING with one of the EXACT same byte length —
// a same-length swap needs no struct/list length fixups anywhere in the blob.
var s3UploadHost = []byte("626727242799-datastore-nex-ecs.s3.amazonaws.com/")

// ourUploadHost is our object-store host+path padded to len(s3UploadHost). The padding
// is a throwaway path segment (the console prepends https://, POSTs there; our catch-all
// handler reads the `key` form field, not the path).
func ourUploadHost() []byte {
	base := storageHostPort + "/"
	if len(base) >= len(s3UploadHost) {
		return []byte(base[:len(s3UploadHost)])
	}
	return append([]byte(base), bytes.Repeat([]byte("a"), len(s3UploadHost)-len(base))...)
}

// rewriteUploadHost swaps the measured S3 bucket host for ours in an upload descriptor.
func rewriteUploadHost(body []byte) []byte {
	return bytes.ReplaceAll(body, s3UploadHost, ourUploadHost())
}

// capturedRelationPID is the pid embedded in every relation object key/name of the
// measured (a player, 0 = 0xdeadbeefdeadbeef). The console builds
// its own asset under ITS pid, so a descriptor carrying a foreign pid is inconsistent
// with what the console expects and it refuses to POST the relation (the course-data
// key has no pid, which is why THAT upload goes through). We rewrite it to the caller's
// pid — a same-length swap (u64 hex is always 16 chars), so no length fixups.
const capturedRelationPID = "deadbeefdeadbeef"

// capturedRelationSize is the asset byte-size baked into each measured relation
// descriptor's object name/key (as lowercase hex, e.g. "..._1ba5_..."). The console
// rejects a descriptor whose size doesn't match the asset it is about to upload (it
// asked for a specific size in the request), so we rewrite the measured size to the
// size the console actually requested. Keys: 1=one-screen 2=entire 3=report 5=clear-check.
var capturedRelationSize = map[uint32]uint32{1: 0x1c000, 2: 0x1ba5, 3: 0x1697, 5: 0xd1a}

// rewriteRelationDescriptor rewrites a method-132 (relation) descriptor for the caller:
// the requested asset size, the embedded pid -> caller's pid, then S3 host -> our store.
func rewriteRelationDescriptor(body []byte, relType uint32, reqSize uint32, pid uint64) []byte {
	if capSize, ok := capturedRelationSize[relType]; ok && reqSize != 0 && reqSize != capSize {
		oldTok := []byte(fmt.Sprintf("_%x_", capSize))
		newTok := []byte(fmt.Sprintf("_%x_", reqSize))
		if len(oldTok) == len(newTok) {
			body = bytes.ReplaceAll(body, oldTok, newTok)
		} else {
			// Differing hex length would shift the enclosing string/struct lengths; a
			// length-aware rebuild is needed. Log so we notice which levels hit this.
			fmt.Printf("[SMM2 Storage] ⚠ relation type=%d size 0x%x->0x%x (len diff, non réécrit)\n", relType, capSize, reqSize)
		}
	}
	body = bytes.ReplaceAll(body, []byte(capturedRelationPID), []byte(fmt.Sprintf("%016x", pid)))
	return rewriteUploadHost(body)
}

// method 132 uploads a course's RELATION DATA, and a course has FOUR distinct ones —
// selected by a type u32 in the request: 1=one-screen thumbnail, 2=entire thumbnail,
// 3=report thumbnail, 5=clear-check replay. Nintendo returns a different presigned
// descriptor (distinct object key) per type; replaying ONE for all four left three
// objects with no valid upload target and hung the console mid-upload. We keep the
// measured descriptor for each type and hand back the matching one.
var m132ByType = map[uint32][]byte{}

var m132TypeName = map[uint32]string{1: "onescreen", 2: "entire", 3: "report", 5: "clearcheck"}

// loadM132Types reads the per-type method-132 descriptors embedded under measured/.
func loadM132Types() {
	for t, name := range m132TypeName {
		if b, err := capturedFS.ReadFile("measured/resp_0x73_m132_" + name + ".bin"); err == nil {
			m132ByType[t] = b
		}
	}
	fmt.Printf("[SMM2 Storage] %d descripteurs relation-data (method 132) chargés\n", len(m132ByType))
}

// smm2PrepareRelationUpload (method 132) returns the presigned upload descriptor for
// the requested relation-data type, with the bucket host rewritten to our object store.
func smm2PrepareRelationUpload(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()           // struct version
	sub := in.Substream() // body: [data_id string][type u32][size u32][...]
	_ = sub.String()      // data_id (as string)
	relType := sub.U32()
	reqSize := sub.U32() // the byte-size of the asset the console will upload

	tmpl := m132ByType[relType]
	if tmpl == nil {
		tmpl = capturedResponses[replayKey(0x73, 132)] // fallback: any measured 132
	}
	if tmpl == nil {
		// Pas de capture : on construit la reponse au lieu d'abandonner la publication.
		return smm2PrepareRelationUploadDynamique(conn, req)
	}
	body := rewriteRelationDescriptor(tmpl, relType, reqSize, conn.PID)
	fmt.Printf("[SMM2 Storage] prepare-relation(132) type=%d(%s) size=0x%x pid=%d -> réécrit (%do)\n", relType, m132TypeName[relType], reqSize, conn.PID, len(body))
	return nex.NewRMCSuccess(s, 0x73, 132, req.CallID, body)
}

// smm2PreparePostObject (24): allocate a data_id, stash the pending course metadata,
// and return DataStoreReqPostInfo {data_id, url, headers, form, root_ca_cert} pointing
// the console at our object store for the blob PUT.
func smm2PreparePostObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // DataStorePreparePostParam struct version
	p := in.Substream()
	size := p.U32()
	name := p.String()
	dataType := p.U16()
	metaBin := p.QBuffer()
	// permission / tags / rating / persistence follow but aren't needed to store a blob.

	id := courses.alloc(conn.PID, name, dataType, metaBin, nil, size)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.U64(id)                // data_id
	body.String(url)            // url
	body.U32(0)                 // headers: none required
	body.U32(0)                 // form: none (simple PUT, not multipart)
	body.Buffer(courses.rootCA) // root_ca_cert (empty on emulator; Nextendo CA in prod)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] prepare_post(24) pid=%d name=%q size=%d -> data_id=%d\n", conn.PID, name, size, id)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
}

// smm2CompletePostObject (26): mark the uploaded course ready (or drop it on failure).
func smm2CompletePostObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()
	success := p.Bool()
	courses.complete(dataID, success)
	fmt.Printf("[SMM2 Storage] complete_post(26) data_id=%d success=%v\n", dataID, success)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2PrepareGetObject (25): return DataStoreReqGetInfo {url, headers, size,
// root_ca_cert, data_id} into our object store for a stored course. For any other
// data_id (the boot/tutorial fetch) replay the measured response so init still proceeds.
func smm2PrepareGetObject(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()
	dataID := p.U64()

	if m := courses.get(dataID); m != nil {
		url := fmt.Sprintf("%s/object/%d", storageURL, dataID)
		body := nex.NewStreamOut(s)
		body.String(url)            // url
		body.U32(0)                 // headers: none
		body.U32(m.Size)            // size
		body.Buffer(courses.rootCA) // root_ca_cert
		body.U64(dataID)            // data_id
		resp := frameStruct(s, 0, body.Bytes())
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d -> %s (%d bytes)\n", dataID, url, m.Size)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
	}

	if body, ok := capturedResponses[replayKey(0x73, 25)]; ok {
		fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d inconnu -> replay measured (boot)\n", dataID)
		return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, body)
	}
	// On DIT pourquoi on refuse. Sans cette ligne, la methode 25 echouait en silence et
	// le journal ne montrait rien du tout — on voyait « erreur de connexion » a l'ecran
	// et aucune trace cote serveur, ce qui est le pire cas pour diagnostiquer.
	fmt.Printf("[SMM2 Storage] prepare_get(25) data_id=%d INTROUVABLE dans le catalogue\n", dataID)
	return nex.NewRMCError(s, 0x73, req.CallID, 0x80690004) // DataStore::NotFound
}

// smm2GetReqGetInfoHeadersInfo (134) : les en-tetes HTTP a employer pour telecharger,
// et leur duree de validite.
//
// Requete : un Uint8, le type de donnee visee.
// Reponse : List<DataStoreKeyValue> puis Uint32 (expiration en secondes).
//
// Notre magasin n'exige aucune en-tete particuliere : la liste est vide, et c'est la
// verite plutot qu'un remplissage. L'expiration est large — les URL que nous servons ne
// sont pas signees et ne perimeront pas.
func smm2GetReqGetInfoHeadersInfo(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	typeDonnee := in.U8()

	// POUR LES IMAGES DE COMMENTAIRE (type 10), LA LISTE VIDE NE SUFFIT PAS.
	//
	// Mesure du 2026-08-24 : la console redemande ces en-tetes toutes les 2,08 secondes,
	// indefiniment, et ne telecharge JAMAIS l'image — c'est le « cadre qui charge sans
	// fin » a l'ecran. La meme reponse vide convient pourtant aux vignettes de niveau
	// (type 2), qui arrivent sans probleme : ce n'est donc pas la forme qui cloche.
	//
	// Le nom de la structure le laissait entendre : kinnay l'appelle
	// CommentPictureReqGetInfo *WithoutHeaders*. La fiche du commentaire est privee
	// d'en-tetes A DESSEIN, et le jeu vient les chercher ici. Lui rendre une liste vide,
	// c'est promettre de les donner et ne rien donner.
	//
	// On ignore lesquelles il attend, d'ou un commutateur :
	//   echo 0 > /opt/smm2/smm2_134.forme   -> aucune (comportement precedent)
	//   echo 1 > ...                        -> Accept: */*            (defaut)
	//   echo 2 > ...                        -> Host: <notre serveur>
	//   echo 3 > ...                        -> les deux
	type entete struct{ cle, val string }
	var entetes []entete
	if typeDonnee == 10 {
		switch formeEssai(134, 1) {
		case 1:
			entetes = []entete{{"Accept", "*/*"}}
		case 2:
			entetes = []entete{{"Host", storageHote()}}
		case 3:
			entetes = []entete{{"Accept", "*/*"}, {"Host", storageHote()}}
		}
	}

	champs := nex.NewStreamOut(s)
	champs.U32(uint32(len(entetes)))
	for _, e := range entetes {
		champs.String(e.cle)
		champs.String(e.val)
	}
	champs.U32(3600) // validite : une heure

	out := nex.NewStreamOut(s)
	if s.StructHeader {
		out.U8(0)
		out.Buffer(champs.Bytes())
	} else {
		out.Write(champs.Bytes())
	}

	fmt.Printf("[SMM2 Storage] get_headers_info(134) type=%d -> %d en-tete(s), 3600s\n", typeDonnee, len(entetes))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2PreparePostObjectCourse (66) : la variante SMM2 de la preparation d'envoi.
//
// Kazu avait implemente la 24, la version GENERIQUE de DataStore. SMM2 n'appelle pas
// celle-la pour publier un niveau : il appelle la 66, avec son propre parametre
// PreparePostCourseParam — deux chaines, puis une longue serie d'entiers, un qBuffer et
// une liste de chaines. Structure relevee dans la documentation PretendoNetwork ; tous
// ses champs y sont marques « Unknown », mais l'ORDRE et les TYPES sont fermes, et
// c'est tout ce qu'il faut pour la traverser sans se decaler.
//
// La REPONSE, elle, est la meme que pour la 24 : DataStoreReqPostInfo. On reutilise
// donc le stockage existant — data_id alloue, URL vers le magasin d'objets — sans rien
// reecrire.
//
// Ce qu'on ne fait pas encore : exploiter les champs du parametre. Le nom du niveau, sa
// description et ses etiquettes sont dedans, et ils finiront dans le catalogue. Pour
// l'instant on veut d'abord voir un fichier arriver sur le disque.
func smm2PreparePostObjectCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8() // version de la structure
	p := in.Substream()

	nom := p.String()  // premiere chaine : le nom saisi par le joueur
	desc := p.String() // seconde : la description
	taille := p.U32()  // taille annoncee du fichier
	_ = p.Bool()
	_ = p.U8()
	_ = p.U8()
	for i := 0; i < 4; i++ {
		_ = p.U32()
	}
	meta := p.QBuffer()
	_ = p.U8()
	_ = p.U32()
	_ = p.U16()
	_ = p.U16()
	_ = p.Bool()
	_ = p.U32()
	_ = p.U32()
	etiquettes := nex.ReadList(p, func(i *nex.StreamIn) string { return i.String() })

	if err := p.Err(); err != nil {
		// Mieux vaut refuser franchement que d'allouer un emplacement pour un niveau
		// qu'on a mal lu : un catalogue avec des entrees fantomes serait pire.
		fmt.Printf("[SMM2 Storage] prepare_post_course(66) pid=%d : parametre illisible (%v), %d octets\n",
			conn.PID, err, len(req.Body))
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002) // DataStore::InvalidArgument
	}

	id := courses.alloc(conn.PID, nom, 0, meta, etiquettes, taille)
	courses.mu.Lock()
	if m := courses.byID[id]; m != nil {
		m.Description = desc
	}
	courses.mu.Unlock()
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.U64(id)
	body.String(url)
	body.U32(0) // pas d'en-tetes
	body.U32(0) // pas de formulaire : simple PUT
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] prepare_post_course(66) pid=%d nom=%q desc=%q taille=%d etiquettes=%v -> data_id=%d\n",
		conn.PID, nom, desc, taille, etiquettes, id)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
}

// smm2PrepareRelationUploadDynamique (132) construit la reponse SANS capture.
//
// Un niveau ne voyage pas seul : SMM2 televerse aussi ses miniatures et l'enregistrement
// de la partie de validation — d'ou les quatre appels consecutifs a cette methode juste
// apres l'envoi du niveau. Sans reponse valide, le jeu annule TOUTE la publication,
// meme si le fichier principal est deja arrive sur le disque. C'est exactement ce qu'on
// observait : 376 971 octets ecrits, et « impossible de publier ».
//
// Structures relevees dans la documentation PretendoNetwork :
//
//	PreparePostRelationObjectParam : String, 4 x Uint32, List<String>
//	RelationObjectReqPostInfo      : String(data_id), String(url), List, List, Buffer
//
// Piege a noter : ici le data_id est une CHAINE, alors que la methode 66 le rend en
// Uint64. Meme notion, deux encodages — les melanger casse la lecture du client.
func smm2PrepareRelationUploadDynamique(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	parent := p.String() // data_id du niveau auquel ce fichier se rattache
	relType := p.U32()   // type : miniature, enregistrement...
	taille := p.U32()
	_ = p.U32()
	_ = p.U32()
	_ = nex.ReadList(p, func(i *nex.StreamIn) string { return i.String() })

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Storage] prepare-relation(132) pid=%d : parametre illisible (%v)\n", conn.PID, err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}

	// Un identifiant propre pour le fichier rattache, distinct du niveau lui-meme.
	id := courses.alloc(conn.PID, fmt.Sprintf("rel-%s-%d", parent, relType), uint16(relType), nil, nil, taille)
	url := fmt.Sprintf("%s/object/%d", storageURL, id)

	body := nex.NewStreamOut(s)
	body.String(fmt.Sprintf("%d", id)) // data_id EN CHAINE, contrairement a la 66
	body.String(url)
	body.U32(0) // headers
	body.U32(0) // form fields
	body.Buffer(courses.rootCA)
	resp := frameStruct(s, 0, body.Bytes())

	fmt.Printf("[SMM2 Storage] prepare-relation(132) pid=%d parent=%s type=%d taille=%d -> data_id=%d\n",
		conn.PID, parent, relType, taille, id)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, resp)
}

// smm2CompletePostObjectsCourse (68) : la confirmation finale de la publication.
//
// Sans elle, tout arrivait sur le disque et RIEN n'etait marque pret : le jeu affichait
// « publie » et le niveau restait invisible, en etat « ready: false ». Il n'y avait pas
// d'erreur a chercher — juste une etape qu'on repondait a vide.
//
// Structure du parametre (documentation PretendoNetwork) :
//
//	5 x String · Uint64 · PreparePostCourseParam
//
// Les cinq chaines sont les identifiants des objets televerses — le niveau et ses
// fichiers rattaches. C'est ce qui relie les cinq blobs en une seule publication.
func smm2CompletePostObjectsCourse(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	in := nex.NewStreamIn(req.Body, s)
	_ = in.U8()
	p := in.Substream()

	var ids []string
	for i := 0; i < 5; i++ {
		if id := p.String(); id != "" {
			ids = append(ids, id)
		}
	}

	// Apres les cinq chaines vient un Uint64 : c'est le data_id du NIVEAU lui-meme.
	// Je le sautais, et le resultat se voyait dans le catalogue — les quatre fichiers
	// rattaches passaient a « pret », le niveau restait a « faux ». La console ne le
	// cite pas parmi les chaines parce qu'il a son propre champ.
	niveau := p.U64()

	if err := p.Err(); err != nil {
		fmt.Printf("[SMM2 Storage] complete_post_course(68) pid=%d : parametre illisible (%v)\n", conn.PID, err)
		return nex.NewRMCError(s, 0x73, req.CallID, 0x00690002)
	}
	if niveau != 0 {
		courses.complete(niveau, true)
	}

	// Chaque objet cite passe a « pret ». On ne devine pas : on marque exactement ce
	// que la console nous dit avoir televerse.
	n := 0
	for _, id := range ids {
		var v uint64
		if _, err := fmt.Sscanf(id, "%d", &v); err == nil && v != 0 {
			courses.complete(v, true)
			n++
		}
	}

	fmt.Printf("[SMM2 Storage] complete_post_course(68) pid=%d -> niveau=%d + %d fichier(s) rattache(s) %v\n",
		conn.PID, niveau, n, ids)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// storageHote rend l'hote du magasin d'objets, sans schema ni chemin.
func storageHote() string {
	h := strings.TrimPrefix(strings.TrimPrefix(storageURL, "https://"), "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	return h
}
