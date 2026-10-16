package main

import (
	"path/filepath"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

func TestNotificationsFlow(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	connOwner := &nex.Connection{Settings: s, PID: 100}
	connPlayer := &nex.Connection{Settings: s, PID: 200}

	// Sauvegarde de l'etat
	oldNotifs := notifications
	oldCourses := courses
	defer func() {
		notifications = oldNotifs
		courses = oldCourses
	}()

	dir := t.TempDir()
	notifications = &magasinNotifications{
		Notifs: map[uint64][]NotificationActivity{},
		Ng:     map[uint64][]NotificationNgCourseInfo{},
		NextID: 1,
		chemin: filepath.Join(dir, "smm2_notifications.json"),
	}
	courses = &courseStore{byID: map[uint64]*courseMeta{}, nextID: 1000}
	courses.byID[50] = &courseMeta{DataID: 50, Name: "Level 1-1", OwnerPID: 100, Ready: true}

	req125 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 125, CallID: 1}
	req126 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 126, CallID: 2}
	req127 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 127, CallID: 3}
	req128 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 128, CallID: 4}
	req129 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 129, CallID: 5}
	req130 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 130, CallID: 6}
	req30 := &nex.RMCMessage{Settings: s, Mode: nex.RMCRequest, Protocol: 0x73, Method: 30, CallID: 7}

	// 1. Initialement, aucune notification pour le proprietaire
	resp125 := smm2GetNewNotification(connOwner, req125)
	if resp125.IsError {
		t.Fatalf("125 should succeed")
	}
	in125 := nex.NewStreamIn(resp125.Body, s)
	if s.StructHeader {
		_ = in125.U8()
		in125 = in125.Substream()
	}
	if in125.Bool() || in125.Bool() || in125.Bool() {
		t.Fatalf("expected all false flags initially")
	}

	// 2. Initialement, liste vide pour 127
	resp127 := smm2GetNotificationList(connOwner, req127)
	in127 := nex.NewStreamIn(resp127.Body, s)
	if s.StructHeader {
		_ = in127.U8()
		in127 = in127.Substream()
	}
	if actCount := in127.U32(); actCount != 0 {
		t.Fatalf("expected 0 activities, got %d", actCount)
	}

	// 3. Joueur 200 joue et aime le niveau 50 de Joueur 100
	notifications.notifierNiveauJoue(100, 200, 50, "Level 1-1")
	notifications.notifierAime(100, 200, 50, "Level 1-1")
	notifications.notifierCommentaire(100, 200, 50, "Level 1-1", "GG!")
	notifications.notifierSuivi(100, 200)

	// 4. Verification de GetNewNotification (125) : hasActivity doit etre true
	resp125 = smm2GetNewNotification(connOwner, req125)
	in125 = nex.NewStreamIn(resp125.Body, s)
	if s.StructHeader {
		_ = in125.U8()
		in125 = in125.Substream()
	}
	hasAct := in125.Bool()
	hasCourses := in125.Bool()
	hasSys := in125.Bool()
	if !hasAct || hasCourses || hasSys {
		t.Fatalf("expected hasAct=true, got act=%v courses=%v sys=%v", hasAct, hasCourses, hasSys)
	}

	// 5. Verification de GetNotificationList (127) : 4 notifications attendues
	resp127 = smm2GetNotificationList(connOwner, req127)
	in127 = nex.NewStreamIn(resp127.Body, s)
	if s.StructHeader {
		_ = in127.U8()
		in127 = in127.Substream()
	}
	actCount := in127.U32()
	if actCount != 4 {
		t.Fatalf("expected 4 activities, got %d", actCount)
	}

	// Verifier la premiere notification (la plus recente : Follow = type 7)
	inAct := in127
	if s.StructHeader {
		_ = in127.U8()
		inAct = in127.Substream()
	}
	_ = inAct.String() // miiName
	notifType := inAct.U8()
	if notifType != notifTypeSuivi {
		t.Fatalf("expected most recent notif to be follow (type %d), got %d", notifTypeSuivi, notifType)
	}
	isRead := inAct.Bool()
	if isRead {
		t.Fatalf("expected notification to be unread")
	}

	// 6. Marquer les notifications comme lues (126)
	resp126 := smm2UpdateNotificationReadStatus(connOwner, req126)
	if resp126.IsError {
		t.Fatalf("126 should succeed")
	}

	// 7. GetNewNotification (125) doit maintenant retourner false
	resp125 = smm2GetNewNotification(connOwner, req125)
	in125 = nex.NewStreamIn(resp125.Body, s)
	if s.StructHeader {
		_ = in125.U8()
		in125 = in125.Substream()
	}
	if in125.Bool() {
		t.Fatalf("expected hasAct=false after mark read")
	}

	// 8. Test Method 128 (UpdateNotificationSettings) -> empty success
	resp128 := smm2UpdateNotificationSettings(connOwner, req128)
	if resp128.IsError || len(resp128.Body) != 0 {
		t.Fatalf("128 should return empty body")
	}

	// 9. Test Method 129 (GetNgCourseNotification) -> empty list
	resp129 := smm2GetNgCourseNotification(connOwner, req129)
	if resp129.IsError {
		t.Fatalf("129 should succeed")
	}
	in129 := nex.NewStreamIn(resp129.Body, s)
	if in129.U32() != 0 {
		t.Fatalf("expected 0 ng courses")
	}

	// 10. Test Method 130 (GetNotificationDetails)
	resp130 := smm2GetNotificationDetails(connOwner, req130)
	if resp130.IsError {
		t.Fatalf("130 should succeed")
	}

	// 11. Test Method 30 (ReceiveNewArrivedNotifications)
	resp30 := smm2ReceiveNewArrivedNotifications(connOwner, req30)
	if resp30.IsError {
		t.Fatalf("30 should succeed")
	}
	in30 := nex.NewStreamIn(resp30.Body, s)
	if in30.U32() != 0 || in30.U32() != 0 {
		t.Fatalf("30 should return 2 empty lists")
	}

	// 12. Test de persistance (Recharger depuis le fichier JSON)
	recharge := &magasinNotifications{
		Notifs: map[uint64][]NotificationActivity{},
		Ng:     map[uint64][]NotificationNgCourseInfo{},
	}
	recharge.charger(dir)
	if len(recharge.Notifs[100]) != 4 {
		t.Fatalf("expected 4 notifications reloaded, got %d", len(recharge.Notifs[100]))
	}

	// 13. Auto-notification ignoree (jouer son propre niveau ne s'auto-notifie pas)
	notifications.notifierNiveauJoue(200, 200, 50, "Level 1-1")
	if len(notifications.activitesPour(200)) != 0 {
		t.Fatalf("self-notification should not be added")
	}
	_ = connPlayer
}
