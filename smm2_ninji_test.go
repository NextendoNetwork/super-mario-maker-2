package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// lecteurNinji relit nos reponses selon la forme MESUREE chez Nintendo (capturas-smm2,
// 2026-10-02), et echoue si un octet manque ou reste. C'est le meme parcours qui relit
// sans reste les 21 fiches d'evenement de Nintendo.
type lecteurNinji struct {
	t  *testing.T
	in *nex.StreamIn
}

func (l lecteurNinji) struct_(nom string, f func(p *nex.StreamIn)) uint8 {
	v := l.in.U8()
	p := l.in.Substream()
	f(p)
	if p.Err() != nil || p.Remaining() != 0 {
		l.t.Fatalf("%s : err=%v, %d octet(s) de reste", nom, p.Err(), p.Remaining())
	}
	return v
}

func lireFiche(t *testing.T, in *nex.StreamIn) (dataID uint64, pb uint32, fantome string) {
	l := lecteurNinji{t, in}
	v := l.struct_("EventCourseInfo", func(p *nex.StreamIn) {
		dataID = p.U64()
		p.String()
		p.String()
		p.U8()
		p.U8()
		p.Bool()
		p.Bool()
		p.DateTime()
		lecteurNinji{t, p}.struct_("DataStoreReqGetInfo", func(q *nex.StreamIn) {
			q.String()
			nex.ReadList(q, func(i *nex.StreamIn) [2]string { return [2]string{i.String(), i.String()} })
			q.U32()
			q.Buffer()
			q.U64()
		})
		for n := p.U32(); n > 0; n-- {
			p.U8()
			p.U32()
		}
		lecteurNinji{t, p}.struct_("UnknownStruct6", func(q *nex.StreamIn) { q.U64(); q.U32() })
		p.U8()
		for _, nom := range []string{"vignette 0x10", "vignette 0x20"} {
			lecteurNinji{t, p}.struct_(nom, func(q *nex.StreamIn) {
				q.String()
				nex.ReadList(q, func(i *nex.StreamIn) [2]string { return [2]string{i.String(), i.String()} })
				q.U32()
				q.Buffer()
				q.String()
			})
		}
		p.DateTime()
		p.U8()
		p.U32()
		p.U16()
		p.U16()
		pb = p.U32()
		p.U32()
		p.U32()
		lecteurNinji{t, p}.struct_("fantome personnel", func(q *nex.StreamIn) {
			fantome = q.String()
			q.U8()
			q.U32()
			q.Buffer()
			q.String()
		})
	})
	if v != 1 {
		t.Fatalf("EventCourseInfo revision %d, mesuree 1", v)
	}
	return
}

func vec(s string) []byte { b, _ := hex.DecodeString(s); return b }

// TestNinjiRejoueLesRequetesDeNintendo : les requetes du jeu, telles que capturees, sur un
// evenement de NOTRE catalogue portant le meme data_id (0x17e2977). Le joueur televerse
// son fantome (132), envoie son temps (102), puis consulte la fiche (85), la liste (86),
// l'histogramme (156), les fantomes (157), ses tampons (153) et l'evenement courant (154).
func TestNinjiRejoueLesRequetesDeNintendo(t *testing.T) {
	dir := t.TempDir()
	c, n, sd, su := courses, ninji, storageDir, storageURL
	defer func() { courses, ninji, storageDir, storageURL = c, n, sd, su }()
	courses = &courseStore{byID: map[uint64]*courseMeta{}, nextID: 1000, catalog: filepath.Join(dir, "catalog.json")}
	storageDir, storageURL = dir, "https://exemple"
	const evt = 0x17e2977
	courses.byID[evt] = &courseMeta{DataID: evt, Name: "Course Nextendo", Description: "d", Size: 376832, Style: 1, Theme: 2}
	os.WriteFile(filepath.Join(dir, "smm2_ninji.json"),
		[]byte(fmt.Sprintf(`[{"data_id": %d, "debut": %d, "fin": %d}]`, evt, time.Now().Add(-time.Hour).Unix(), time.Now().Add(time.Hour).Unix())), 0o644)
	ninji = &magasinNinji{resultats: map[uint64]map[uint64]*resultatNinji{}}
	ninji.charger(dir)

	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}
	appel := func(m uint32, corps []byte) *nex.RMCMessage {
		return &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: m, CallID: 1, Body: corps}
	}

	// En production il peut y avoir des 132 capturees a rejouer : on en pose une, pour
	// verifier qu'un fantome ne la prend PAS (sa clef designerait un autre fichier).
	precedente, avait := capturedResponses[replayKey(0x73, 132)]
	capturedResponses[replayKey(0x73, 132)] = vec("00140000000c00636c65662d6361707475726565000000000000000000000000")
	defer func() {
		if avait {
			capturedResponses[replayKey(0x73, 132)] = precedente
		} else {
			delete(capturedResponses, replayKey(0x73, 132))
		}
	}()

	// 132 : le parametre de Nintendo — parent "25045367" (= 0x17e2977), type 13, 3187 octets.
	r132 := smm2PrepareRelationUpload(conn, appel(132, vec("003800000009003235303435333637000d000000730c00000000000040000000060000000800556e6b6e6f776e00010000010000010000010000010000")))
	in := nex.NewStreamIn(r132.Body, s)
	_ = in.U8()
	clef := in.Substream().String()
	var fantome uint64
	if _, err := fmt.Sscanf(clef, "%d", &fantome); err != nil {
		t.Fatalf("132 : clef %q, attendu notre identifiant d'objet", clef)
	}
	os.WriteFile(blobPath(fantome), make([]byte, 3187), 0o644)

	// 102 : la premiere structure de Nintendo telle quelle (temps 56359 ms), la seconde
	// avec NOTRE clef a la place de la leur.
	s2 := nex.NewStreamOut(s)
	s2.String(clef)
	s2.Write(vec("f0022000"))
	corps102 := append(vec("012e00000077297e01000000000200000027dc000001000d00000077297e010000000090037000000100000500000001010000"), 1)
	corps102 = append(corps102, nex.NewStreamOut(s).Bytes()...)
	b := nex.NewStreamOut(s)
	b.Buffer(s2.Bytes())
	corps102 = append(corps102, b.Bytes()...)
	if r := smm2PostPlayResultEventCourse(conn, appel(102, corps102)); r.IsError || len(r.Body) != 0 {
		t.Fatalf("102 : %+v, attendu un succes vide comme chez Nintendo", r)
	}
	if r := ninji.resultat(evt, 1); r == nil || r.TempsMs != 56359 || r.Fantome != fantome || r.Taille != 3187 {
		t.Fatalf("102 : resultat enregistre %+v", r)
	}

	// 85 : la fiche porte le meilleur temps et la reference du fantome.
	r85 := smm2GetCoursesEvent(conn, appel(85, vec("00100000000100000077297e01000000003b0200000000000000")))
	in = nex.NewStreamIn(r85.Body, s)
	if in.U32() != 1 {
		t.Fatal("85 : attendu une fiche")
	}
	id, pb, url := lireFiche(t, in)
	if id != evt || pb != 56359 || url != fmt.Sprintf("https://exemple/object/%d", fantome) {
		t.Fatalf("85 : fiche %x, meilleur temps %d, fantome %q", id, pb, url)
	}
	if in.U32() != 1 || in.Result() != 0x00690001 || in.Remaining() != 0 {
		t.Fatal("85 : resultats mal formes")
	}

	// 86 : la liste, relue sans reste.
	r86 := smm2SearchCoursesEvent(conn, appel(86, vec("00040000003b020000")))
	in = nex.NewStreamIn(r86.Body, s)
	if in.U32() != 1 {
		t.Fatal("86 : attendu un evenement")
	}
	lireFiche(t, in)
	if in.Remaining() != 0 {
		t.Fatalf("86 : %d octet(s) de reste", in.Remaining())
	}

	// 156 : 110 cases, notre temps dans la 46e (56 s), trois seuils.
	r156 := smm2GetEventCourseHistogram(conn, appel(156, vec("000800000077297e0100000000")))
	in = nex.NewStreamIn(r156.Body, s)
	lecteurNinji{t, in}.struct_("EventCourseHistogram", func(p *nex.StreamIn) {
		if p.U64() != evt || p.U32() != 10000 || p.U32() != 120000 || p.U32() != 1000 {
			t.Fatal("156 : en-tete different de la mesure")
		}
		cases := nex.ReadList(p, func(i *nex.StreamIn) uint32 { return i.U32() })
		if len(cases) != 110 || cases[46] != 1 {
			t.Fatalf("156 : %d cases, case 46 = %d", len(cases), cases[46])
		}
		if p.U32() != 3 {
			t.Fatal("156 : attendu trois seuils")
		}
		for _, cle := range []uint8{10, 30, 50} {
			if k, v := p.U8(), p.U32(); k != cle || v != 56359 {
				t.Fatalf("156 : seuil %d=%d", k, v)
			}
		}
		p.U32()
	})

	// 157 : le fantome, au format EventCourseGhostInfo.
	r157 := smm2GetEventCourseGhost(conn, appel(157, vec("000d00000077297e010000000090de000032")))
	in = nex.NewStreamIn(r157.Body, s)
	if in.U32() != 1 {
		t.Fatal("157 : attendu un fantome")
	}
	lecteurNinji{t, in}.struct_("EventCourseGhostInfo", func(p *nex.StreamIn) {
		lecteurNinji{t, p}.struct_("RelationObjectReqGetInfo", func(q *nex.StreamIn) {
			q.String()
			if q.U8() != 40 || q.U32() != 3187 {
				t.Fatal("157 : type ou taille du fantome")
			}
			q.Buffer()
			q.String()
		})
		if p.U32() != 56359 || p.PID() != 1 {
			t.Fatal("157 : temps ou joueur")
		}
	})

	// 153 et 154.
	if r := smm2GetEventCourseStamp(conn, appel(153, nil)); nex.NewStreamIn(r.Body, s).U32() != 1 {
		t.Fatal("153 : attendu un tampon")
	}
	in = nex.NewStreamIn(smm2GetEventCourseStatus(conn, appel(154, nil)).Body, s)
	lecteurNinji{t, in}.struct_("EventCourseStatusInfo", func(p *nex.StreamIn) {
		if p.U64() != evt || p.Bool() || p.U64() != dateTimeEpoque {
			t.Fatal("154 : evenement courant")
		}
	})

	// Un temps moins bon ne remplace pas le meilleur.
	if ninji.enregistrer(evt, 1, 60000, 0, 0) || ninji.resultat(evt, 1).TempsMs != 56359 {
		t.Fatal("un temps moins bon a remplace le meilleur")
	}
}

// TestNinjiSansFichierRienNeChange : sans smm2_ninji.json, la 154 rend zero et la 86 une
// liste vide — le comportement d'avant ce fichier.
func TestNinjiSansFichierRienNeChange(t *testing.T) {
	n := ninji
	defer func() { ninji = n }()
	ninji = &magasinNinji{resultats: map[uint64]map[uint64]*resultatNinji{}}
	ninji.charger(t.TempDir())
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	conn := &nex.Connection{Settings: s, PID: 1}
	r := smm2SearchCoursesEvent(conn, &nex.RMCMessage{Settings: s, Method: 86, Body: vec("00040000003b020000")})
	if fmt.Sprintf("%x", r.Body) != "00000000" {
		t.Fatalf("86 sans evenement : %x", r.Body)
	}
	in := nex.NewStreamIn(smm2GetEventCourseStatus(conn, &nex.RMCMessage{Settings: s, Method: 154}).Body, s)
	_ = in.U8()
	if in.Substream().U64() != 0 {
		t.Fatal("154 sans evenement : un evenement courant")
	}
}
