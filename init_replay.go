package main

// SMM2 online-init for DataStore (0x73) and Utility (0x6E) — public build.
//
// A full server-side DataStore/Utility implementation for SMM2's Course World is not part
// of this tree. The handlers below answer every method with a valid empty response so the
// game proceeds through online bring-up and the live log reveals which methods it calls,
// to be implemented over time. No response bodies are shipped in the public build.

import (
	"embed"
	"fmt"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// capturedResponses maps (protocolID<<16 | methodID) -> a response body. Empty in the
// public build; handlers fall through to the empty-response path when a key is absent.
// capturedFS is empty in the public build (no measured bodies shipped).
var capturedFS embed.FS

var capturedResponses = map[uint32][]byte{}

func replayKey(proto uint16, method uint32) uint32 { return uint32(proto)<<16 | method }

// loadCapturedResponses is a no-op in the public build (no bodies shipped).
func loadCapturedResponses() {}

// replayHandler answers a protocol's methods. 0x73.8 = DataStore::NotFound (0x80690004),
// handled gracefully by SMM2; any other method returns an empty-list success (count = 0).
func replayHandler(proto uint16) nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		if proto == 0x73 && req.Method == 8 {
			return nex.NewRMCError(s, proto, req.CallID, 0x80690004)
		}
		if body, ok := capturedResponses[replayKey(proto, req.Method)]; ok {
			return nex.NewRMCSuccess(s, proto, req.Method, req.CallID, body)
		}
		out := nex.NewStreamOut(s)
		out.U32(0)
		fmt.Printf("[SMM2] 0x%02x.%d callID=%d -> empty-list fallback (stub)\n", proto, req.Method, req.CallID)
		return nex.NewRMCSuccess(s, proto, req.Method, req.CallID, out.Bytes())
	}
}

// setupSMM2InitReplay registers DataStore (0x73) + Utility (0x6E) stub handlers.
func setupSMM2InitReplay(endpoint *nex.Endpoint) {
	loadCapturedResponses()
	// Relire les profils SMM2 ecrits sur le disque. Sans cela, chaque redeploiement
	// obligeait le joueur a refaire son Mii, son nom et son pays.
	nex.SMM2ChargerProfils()
	endpoint.Register(0x73, smm2DataStoreHandler())
	endpoint.Register(0x6E, utilityAvecRepli())
	fmt.Println("[SMM2] DataStore(0x73) + Utility(0x6E) stub registered (no bodies in public build)")
}

// utilityAvecRepli : le vrai handler Utility, avec le stub en filet.
//
// CE QUE CELA NE CORRIGE PAS, D'ABORD. Ce n'est PAS le correctif du cooperatif. La
// methode que le jeu interroge en boucle au moment de l'echec est GetIntegerSettings
// (0x6E.7), et le stub y repondait deja U32(0) — exactement les memes octets que le vrai
// handler. Changer de handler ne modifie pas un seul octet de cette reponse.
//
// CE QUE CELA CORRIGE. Le stub repondait U32(0) a TOUTES les methodes de Utility, dont
// AcquireNexUniqueID (0x6E.1) et AcquireNexUniqueIDWithPassword (0x6E.2), ou le vrai
// handler rend un identifiant reel. Le commentaire de nextendo-nex est net a ce sujet :
// un jeu ne demande cet identifiant qu'a la PREMIERE connexion d'un compte, puis le
// garde en local — d'ou le fait qu'une reponse fausse « marche pour presque tout le
// monde » et casse uniquement les comptes neufs. Splatoon 2 affiche 2306-0103 dans ce
// cas. SMM2 etait le seul des neuf jeux a couvrir tout Utility d'un stub.
//
// C'est donc de l'hygiene sur un defaut latent, pas une reponse au probleme du jour.
//
// LE REPLI EXISTE PARCE QUE LE VRAI HANDLER REFUSE CE QU'IL NE CONNAIT PAS : il rend
// notImplemented, la ou le stub rendait une liste vide. SMM2 n'appelle aujourd'hui que
// la 7, mais echanger un stub inoffensif contre un refus sur une methode inconnue serait
// prendre un risque pour rien.
func utilityAvecRepli() nex.RMCHandler {
	vrai := nex.UtilityHandler()
	stub := replayHandler(0x6E)
	connues := map[uint32]bool{
		nex.MethodAcquireNexUniqueID:             true,
		nex.MethodAcquireNexUniqueIDWithPassword: true,
		nex.MethodGetIntegerSettings:             true,
		nex.MethodGetStringSettings:              true,
	}
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		if connues[req.Method] {
			return vrai(conn, req)
		}
		return stub(conn, req)
	}
}
