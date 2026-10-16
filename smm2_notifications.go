package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Types d'activites de notification dans Super Mario Maker 2 :
const (
	notifTypeJoue          uint8 = 1 // Joueur a joue a votre niveau
	notifTypeReussi        uint8 = 2 // Joueur a reussi votre niveau
	notifTypeAime          uint8 = 3 // Joueur a aime votre niveau (« J'aime ! »)
	notifTypeCommentaire   uint8 = 4 // Joueur a commente votre niveau
	notifTypeRecord        uint8 = 5 // Joueur a battu le record mondial de votre niveau
	notifTypePremierSucces uint8 = 6 // Joueur a realise le premier succes de votre niveau
	notifTypeSuivi         uint8 = 7 // Joueur a commence a vous suivre
)

// NotificationActivity represente une entree d'activite recue par un createur.
// Structure reconstituee depuis la routine de deserialisation 0x5efcc0 du binaire SMM2.
type NotificationActivity struct {
	ID            uint64 `json:"id"`
	RecipientPID  uint64 `json:"recipient_pid"`
	MiiName       string `json:"mii_name"`
	Type          uint8  `json:"type"`
	IsRead        bool   `json:"is_read"`
	CourseID      uint64 `json:"course_id"`
	CourseTitle   string `json:"course_title"`
	SenderPID     uint64 `json:"sender_pid"`
	SenderCountry string `json:"sender_country"`
	Param1Type    uint32 `json:"param1_type"`
	Param1ID      uint64 `json:"param1_id"`
	Param1Text    string `json:"param1_text"`
	Param2Type    uint32 `json:"param2_type"`
	Param2ID      uint64 `json:"param2_id"`
	Param2Text    string `json:"param2_text"`
	Param3Type    uint32 `json:"param3_type"`
	Param3ID      uint64 `json:"param3_id"`
	Param3Text    string `json:"param3_text"`
	ExtraU32      uint32 `json:"extra_u32"`
	Timestamp     int64  `json:"timestamp"`
}

// NotificationNgCourseInfo represente la notification d'un niveau supprime ou modere.
// Structure reconstituee depuis 0x5f0d00 du binaire SMM2.
type NotificationNgCourseInfo struct {
	DataID      uint64 `json:"data_id"`
	NgType      uint32 `json:"ng_type"`
	CourseName  string `json:"course_name"`
	CanReupload bool   `json:"can_reupload"`
	Details     string `json:"details"`
}

type magasinNotifications struct {
	mu     sync.RWMutex
	Notifs map[uint64][]NotificationActivity `json:"notifs"`
	Ng     map[uint64][]NotificationNgCourseInfo `json:"ng"`
	NextID uint64                            `json:"next_id"`
	chemin string
}

var notifications = &magasinNotifications{
	Notifs: map[uint64][]NotificationActivity{},
	Ng:     map[uint64][]NotificationNgCourseInfo{},
	NextID: 1,
}

func (m *magasinNotifications) charger(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chemin = filepath.Join(dir, "smm2_notifications.json")
	if b, err := os.ReadFile(m.chemin); err == nil {
		var c magasinNotifications
		if json.Unmarshal(b, &c) == nil {
			if c.Notifs != nil {
				m.Notifs = c.Notifs
			}
			if c.Ng != nil {
				m.Ng = c.Ng
			}
			if c.NextID != 0 {
				m.NextID = c.NextID
			}
		}
	}
	total := 0
	for _, l := range m.Notifs {
		total += len(l)
	}
	fmt.Printf("[SMM2 Notifications] %d notification(s) chargee(s) pour %d joueur(s)\n", total, len(m.Notifs))
}

func (m *magasinNotifications) ecrireLocked() {
	if m.chemin == "" {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	tmp := m.chemin + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		_ = os.Rename(tmp, m.chemin)
	}
}

const maxNotificationsParJoueur = 100

func (m *magasinNotifications) ajouter(n NotificationActivity) {
	if n.RecipientPID == 0 || n.RecipientPID == n.SenderPID {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	n.ID = m.NextID
	m.NextID++
	if n.Timestamp == 0 {
		n.Timestamp = time.Now().Unix()
	}

	l := m.Notifs[n.RecipientPID]
	// Insertion en tete (plus recente en premier)
	m.Notifs[n.RecipientPID] = append([]NotificationActivity{n}, l...)
	if len(m.Notifs[n.RecipientPID]) > maxNotificationsParJoueur {
		m.Notifs[n.RecipientPID] = m.Notifs[n.RecipientPID][:maxNotificationsParJoueur]
	}
	m.ecrireLocked()
}

func (m *magasinNotifications) aNouveautes(pid uint64) (activite, cours, systeme bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.Notifs[pid] {
		if !n.IsRead {
			activite = true
			break
		}
	}
	return activite, false, false
}

func (m *magasinNotifications) marquerLues(pid uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	modifie := false
	for i := range m.Notifs[pid] {
		if !m.Notifs[pid][i].IsRead {
			m.Notifs[pid][i].IsRead = true
			modifie = true
		}
	}
	if modifie {
		m.ecrireLocked()
	}
}

func (m *magasinNotifications) activitesPour(pid uint64) []NotificationActivity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l := m.Notifs[pid]
	copie := make([]NotificationActivity, len(l))
	copy(copie, l)
	return copie
}

func (m *magasinNotifications) ngPour(pid uint64) []NotificationNgCourseInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l := m.Ng[pid]
	copie := make([]NotificationNgCourseInfo, len(l))
	copy(copie, l)
	return copie
}

// Helpers de creation de notification

func paysJoueur(pid uint64) string {
	if pays := paysComptes.afficher(pid, "US"); pays != "" {
		return pays
	}
	return "US"
}

func (m *magasinNotifications) notifierNiveauJoue(auteurPID, joueurPID, dataID uint64, nomNiveau string) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypeJoue,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
	})
}

func (m *magasinNotifications) notifierNiveauReussi(auteurPID, joueurPID, dataID uint64, nomNiveau string) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypeReussi,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
	})
}

func (m *magasinNotifications) notifierPremierSucces(auteurPID, joueurPID, dataID uint64, nomNiveau string) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypePremierSucces,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
	})
}

func (m *magasinNotifications) notifierRecord(auteurPID, joueurPID, dataID uint64, nomNiveau string, ms uint32) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypeRecord,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
		Param1Type:    1,
		Param1ID:      uint64(ms),
	})
}

func (m *magasinNotifications) notifierAime(auteurPID, joueurPID, dataID uint64, nomNiveau string) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypeAime,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
	})
}

func (m *magasinNotifications) notifierCommentaire(auteurPID, joueurPID, dataID uint64, nomNiveau string, texte string) {
	m.ajouter(NotificationActivity{
		RecipientPID:  auteurPID,
		SenderPID:     joueurPID,
		MiiName:       pseudoOr(joueurPID),
		Type:          notifTypeCommentaire,
		CourseID:      dataID,
		CourseTitle:   nomNiveau,
		SenderCountry: paysJoueur(joueurPID),
		Param1Text:    texte,
	})
}

func (m *magasinNotifications) notifierSuivi(createurPID, suiveurPID uint64) {
	m.ajouter(NotificationActivity{
		RecipientPID:  createurPID,
		SenderPID:     suiveurPID,
		MiiName:       pseudoOr(suiveurPID),
		Type:          notifTypeSuivi,
		SenderCountry: paysJoueur(suiveurPID),
	})
}

// Serialisation d'une activite de notification selon 0x5efcc0
func ecrireNotificationActivity(out *nex.StreamOut, n NotificationActivity) {
	s := out.Settings
	e := nex.NewStreamOut(s)

	e.String(n.MiiName)
	e.U8(n.Type)
	e.Bool(n.IsRead)
	e.U64(n.CourseID)
	e.String(n.CourseTitle)
	e.U64(s.Publique(n.SenderPID))
	e.String(n.SenderCountry)

	// Param 1 (u32, u64, string)
	e.U32(n.Param1Type)
	e.U64(n.Param1ID)
	e.String(n.Param1Text)

	// Param 2 (u32, u64, string)
	e.U32(n.Param2Type)
	e.U64(n.Param2ID)
	e.String(n.Param2Text)

	// Param 3 (u32, u64, string)
	e.U32(n.Param3Type)
	e.U64(n.Param3ID)
	e.String(n.Param3Text)

	e.U32(n.ExtraU32)
	e.DateTime(dateTimeDe(n.Timestamp))

	out.Write(frameStruct(s, 0, e.Bytes()))
}

func dateTimeDe(unix int64) uint64 {
	if unix == 0 {
		return dateTimeEpoque
	}
	an, mois, jour, h, mi, se := dateDe(unix)
	return uint64(nex.MakeDateTime(an, mois, jour, h, mi, se))
}

// --- Handlers NEX Protocol 0x73 ---

// smm2GetNewNotification (125) : le jeu demande s'il y a de nouvelles notifications.
// Requete : vide.
// Reponse : struct{ hasActivity bool, hasCourses bool, hasSystem bool }
func smm2GetNewNotification(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	hasAct, hasCourses, hasSystem := notifications.aNouveautes(conn.PID)

	inner := nex.NewStreamOut(s)
	inner.Bool(hasAct)
	inner.Bool(hasCourses)
	inner.Bool(hasSystem)

	fmt.Printf("[SMM2 Notifications] get_new_notification(125) pid=%d -> act=%v cours=%v sys=%v\n",
		conn.PID, hasAct, hasCourses, hasSystem)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, inner.Bytes()))
}

// smm2UpdateNotificationReadStatus (126) : marque les notifications comme lues.
// Requete : struct{ category uint32 }
// Reponse : vide.
func smm2UpdateNotificationReadStatus(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	notifications.marquerLues(conn.PID)
	fmt.Printf("[SMM2 Notifications] mark_notification_read(126) pid=%d\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, nil)
}

// smm2GetNotificationList (127) : le jeu demande la liste complete des notifications pour les 3 onglets.
// Requete : struct{ category uint8 }
// Reponse : struct{ List<NotificationActivity>, List<CourseNotification>, List<SystemNotification> }
func smm2GetNotificationList(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	acts := notifications.activitesPour(conn.PID)

	inner := nex.NewStreamOut(s)

	// Liste 1 : Activites
	inner.U32(uint32(len(acts)))
	for _, a := range acts {
		ecrireNotificationActivity(inner, a)
	}

	// Liste 2 : Nouveaux niveaux des createurs suivis (vide pour l'instant)
	inner.U32(0)

	// Liste 3 : Annonces systeme / officielles (vide pour l'instant)
	inner.U32(0)

	fmt.Printf("[SMM2 Notifications] get_notification_list(127) pid=%d -> %d activite(s)\n", conn.PID, len(acts))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, inner.Bytes()))
}

// smm2UpdateNotificationSettings (128) : mise a jour des reglages de notification.
// Requete : struct{ 4 chaines }
// Reponse : vide.
func smm2UpdateNotificationSettings(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	fmt.Printf("[SMM2 Notifications] update_notification_settings(128) pid=%d\n", conn.PID)
	return nex.NewRMCSuccess(conn.Settings, 0x73, req.Method, req.CallID, nil)
}

// smm2GetNgCourseNotification (129) : notifications relatives aux niveaux moderes/supprimes.
// Requete : vide.
// Reponse : List<NotificationNgCourseInfo>
func smm2GetNgCourseNotification(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	ngs := notifications.ngPour(conn.PID)

	out := nex.NewStreamOut(s)
	out.U32(uint32(len(ngs)))
	for _, ng := range ngs {
		e := nex.NewStreamOut(s)
		e.U64(ng.DataID)
		e.U32(ng.NgType)
		e.String(ng.CourseName)
		e.Bool(ng.CanReupload)
		e.String(ng.Details)
		out.Write(frameStruct(s, 0, e.Bytes()))
	}

	fmt.Printf("[SMM2 Notifications] get_ng_course_notification(129) pid=%d -> %d niveau(x) modere(s)\n", conn.PID, len(ngs))
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}

// smm2GetNotificationDetails (130) : details d'une notification.
// Reponse : struct{ u32, string, string, bool, DateTime }
func smm2GetNotificationDetails(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	inner := nex.NewStreamOut(s)
	inner.U32(0)
	inner.String("")
	inner.String("")
	inner.Bool(false)
	inner.U64(dateTimeEpoque)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, frameStruct(s, 0, inner.Bytes()))
}

// smm2ReceiveNewArrivedNotifications (30) : notifications du protocole DataStore standard.
// Requete : struct{ uint64, List<uint16> }
// Reponse : List<DataStoreNotification>, List<Result>
func smm2ReceiveNewArrivedNotifications(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
	s := conn.Settings
	out := nex.NewStreamOut(s)
	listeVide(out) // List<DataStoreNotification>
	listeVide(out) // List<Result>
	fmt.Printf("[SMM2 Notifications] receive_new_arrived_notifications(30) pid=%d -> vide\n", conn.PID)
	return nex.NewRMCSuccess(s, 0x73, req.Method, req.CallID, out.Bytes())
}
