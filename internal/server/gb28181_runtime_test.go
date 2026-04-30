package server

import (
	"testing"
	"time"

	"maas-box/internal/gb28181"
	"maas-box/internal/model"
)

func TestGBGetDeviceForAuthRejectBlockedUnknownDevice(t *testing.T) {
	s := newFocusedTestServer(t)
	store := &gb28181Store{db: s.db, cfg: s.cfg}

	deviceID := "34020000001320000001"
	if err := s.db.Create(&model.GBDeviceBlock{
		DeviceID: deviceID,
		Reason:   "deleted by user",
	}).Error; err != nil {
		t.Fatalf("create gb block failed: %v", err)
	}

	authDevice, err := store.GetDeviceForAuth(deviceID)
	if err != nil {
		t.Fatalf("GetDeviceForAuth failed: %v", err)
	}
	if authDevice.DeviceID != deviceID {
		t.Fatalf("expected device_id=%s, got=%s", deviceID, authDevice.DeviceID)
	}
	if authDevice.Enabled {
		t.Fatalf("expected blocked device enabled=false")
	}
}

func TestReconcileAllGBSourceStatusFixesDrift(t *testing.T) {
	s := newFocusedTestServer(t)

	deviceID := "34020000001110103920"
	channelID := "34020000001310000010"
	streamID := deviceID + "_" + channelID
	deviceSourceID := "gb-device-source-drift-1"
	channelSourceID := "gb-channel-source-drift-1"

	deviceSource := model.MediaSource{
		ID:              deviceSourceID,
		Name:            "GB Device Drift",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindDevice,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "gb28181",
		StreamID:        deviceID,
		StreamURL:       "gb28181://" + deviceID,
		Status:          "online",
		AIStatus:        model.DeviceAIStatusIdle,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&deviceSource).Error; err != nil {
		t.Fatalf("create gb device source failed: %v", err)
	}

	channelSource := model.MediaSource{
		ID:              channelSourceID,
		Name:            "GB Channel Drift",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "rtp",
		StreamID:        streamID,
		StreamURL:       "gb28181://" + deviceID + "/" + channelID,
		ParentID:        deviceSourceID,
		Status:          "online",
		AIStatus:        model.DeviceAIStatusIdle,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&channelSource).Error; err != nil {
		t.Fatalf("create gb channel source failed: %v", err)
	}

	gbDevice := model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: deviceSourceID,
		Name:           "GB Device Drift",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "offline",
		Transport:      "udp",
		Expires:        3600,
	}
	if err := s.db.Create(&gbDevice).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	gbChannel := model.GBChannel{
		DeviceID:        deviceID,
		ChannelID:       channelID,
		SourceIDChannel: channelSourceID,
		Name:            "GB Channel Drift",
		Status:          "ON",
	}
	if err := s.db.Create(&gbChannel).Error; err != nil {
		t.Fatalf("create gb channel failed: %v", err)
	}

	if err := s.reconcileAllGBSourceStatus(); err != nil {
		t.Fatalf("reconcile gb source status failed: %v", err)
	}

	var updatedDeviceSource model.MediaSource
	if err := s.db.Where("id = ?", deviceSourceID).First(&updatedDeviceSource).Error; err != nil {
		t.Fatalf("query updated device source failed: %v", err)
	}
	if updatedDeviceSource.Status != "online" {
		t.Fatalf("expected gb device source status unchanged, got=%s", updatedDeviceSource.Status)
	}

	var updatedChannelSource model.MediaSource
	if err := s.db.Where("id = ?", channelSourceID).First(&updatedChannelSource).Error; err != nil {
		t.Fatalf("query updated channel source failed: %v", err)
	}
	if updatedChannelSource.Status != "online" {
		t.Fatalf("expected gb channel source status unchanged, got=%s", updatedChannelSource.Status)
	}
}

func TestMarkRegisteredQueuesAutoInviteWhenDeviceAlreadyRunning(t *testing.T) {
	s := newFocusedTestServer(t)
	s.gbService = &gb28181.Service{}
	deviceID := "34020000001320000999"
	s.gbInviteRunning = map[string]bool{deviceID: true}
	s.gbInvitePending = map[string]bool{}
	store := &gb28181Store{db: s.db, cfg: s.cfg, srv: s}

	err := store.MarkRegistered(gb28181.RegisterEvent{
		DeviceID:     deviceID,
		Transport:    "udp",
		SourceAddr:   "127.0.0.1:5060",
		Expires:      3600,
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("MarkRegistered failed: %v", err)
	}
	if !s.gbInvitePending[deviceID] {
		t.Fatalf("expected auto invite pending for device=%s", deviceID)
	}
}

func TestUpsertCatalogQueuesAutoInviteWhenDeviceAlreadyRunning(t *testing.T) {
	s := newFocusedTestServer(t)
	s.gbService = &gb28181.Service{}
	deviceID := "34020000001320000888"
	channelID := "34020000001310000888"
	s.gbInviteRunning = map[string]bool{deviceID: true}
	s.gbInvitePending = map[string]bool{}
	store := &gb28181Store{db: s.db, cfg: s.cfg, srv: s}

	err := store.UpsertCatalog(gb28181.CatalogEvent{
		DeviceID:   deviceID,
		OccurredAt: time.Now(),
		Channels: []gb28181.CatalogChannel{
			{ChannelID: channelID, Name: "ch-888"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertCatalog failed: %v", err)
	}
	if !s.gbInvitePending[deviceID] {
		t.Fatalf("expected auto invite pending for device=%s", deviceID)
	}
}

func TestGBSignalEventsDoNotOverrideMediaSourceStatus(t *testing.T) {
	s := newFocusedTestServer(t)
	store := &gb28181Store{db: s.db, cfg: s.cfg}

	deviceID := "34020000001110105555"
	channelID := "34020000001310005555"
	deviceSourceID := "gb-device-source-signal-stability"
	channelSourceID := "gb-channel-source-signal-stability"

	deviceSource := model.MediaSource{
		ID:              deviceSourceID,
		Name:            "GB Device Status Stable",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindDevice,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "gb28181",
		StreamID:        deviceID,
		StreamURL:       "gb28181://" + deviceID,
		Status:          "online",
		AIStatus:        model.DeviceAIStatusIdle,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&deviceSource).Error; err != nil {
		t.Fatalf("create gb device source failed: %v", err)
	}

	channelSource := model.MediaSource{
		ID:              channelSourceID,
		Name:            "GB Channel Status Stable",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "rtp",
		StreamID:        buildGBChannelSourceStreamID(deviceID, channelID),
		StreamURL:       "gb28181://" + deviceID + "/" + channelID,
		ParentID:        deviceSourceID,
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&channelSource).Error; err != nil {
		t.Fatalf("create gb channel source failed: %v", err)
	}

	gbDevice := model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: deviceSourceID,
		Name:           "GB Device Signal Stable",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "offline",
		Transport:      "udp",
		Expires:        3600,
	}
	if err := s.db.Create(&gbDevice).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	gbChannel := model.GBChannel{
		DeviceID:        deviceID,
		ChannelID:       channelID,
		SourceIDChannel: channelSourceID,
		Name:            "GB Channel Signal Stable",
		Status:          "ON",
	}
	if err := s.db.Create(&gbChannel).Error; err != nil {
		t.Fatalf("create gb channel failed: %v", err)
	}

	assertSourceStatuses := func(expectDeviceStatus, expectChannelStatus string) {
		t.Helper()
		var ds model.MediaSource
		if err := s.db.Where("id = ?", deviceSourceID).First(&ds).Error; err != nil {
			t.Fatalf("query device source failed: %v", err)
		}
		if ds.Status != expectDeviceStatus {
			t.Fatalf("expected device source status=%s, got=%s", expectDeviceStatus, ds.Status)
		}
		var cs model.MediaSource
		if err := s.db.Where("id = ?", channelSourceID).First(&cs).Error; err != nil {
			t.Fatalf("query channel source failed: %v", err)
		}
		if cs.Status != expectChannelStatus {
			t.Fatalf("expected channel source status=%s, got=%s", expectChannelStatus, cs.Status)
		}
	}

	now := time.Now()
	if err := store.MarkRegistered(gb28181.RegisterEvent{
		DeviceID:     deviceID,
		Transport:    "udp",
		SourceAddr:   "127.0.0.1:5060",
		Expires:      3600,
		RegisteredAt: now,
	}); err != nil {
		t.Fatalf("MarkRegistered failed: %v", err)
	}
	assertSourceStatuses("online", "offline")

	if err := store.MarkKeepalive(gb28181.KeepaliveEvent{
		DeviceID:    deviceID,
		Transport:   "udp",
		SourceAddr:  "127.0.0.1:5060",
		KeepaliveAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("MarkKeepalive failed: %v", err)
	}
	assertSourceStatuses("online", "offline")

	if err := store.UpsertCatalog(gb28181.CatalogEvent{
		DeviceID:   deviceID,
		OccurredAt: now.Add(6 * time.Second),
		Channels: []gb28181.CatalogChannel{
			{ChannelID: channelID, Name: "GB Channel Signal Stable", Status: "ON"},
		},
	}); err != nil {
		t.Fatalf("UpsertCatalog failed: %v", err)
	}
	assertSourceStatuses("online", "offline")
}
