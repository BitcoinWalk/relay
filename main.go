package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/policies"
	"go.etcd.io/bbolt"
)

const adminHex = "90cf043861e5b5a9972cb7b529a5ba71b215d6d1e314c749d5526ec133f1db73"

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseWriters(value string) (map[nostr.PubKey]bool, error) {
	writers := map[nostr.PubKey]bool{}
	for _, value := range strings.Split(value, ",") {
		key, err := nostr.PubKeyFromHex(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid writer public key: %w", err)
		}
		writers[key] = true
	}
	return writers, nil
}

func validateListenAddress(listen string, allowContainer bool) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("RELAY_LISTEN must use a loopback IP; public access requires a TLS reverse proxy")
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || allowContainer && ip.Equal(net.IPv4zero)) {
		return nil
	}
	return errors.New("RELAY_LISTEN must use a loopback IP; public access requires a TLS reverse proxy")
}

func replicaReceiverContainerListenAllowed(flag, organizerMode, city, service, destination string) (bool, error) {
	if flag != "false" && flag != "true" {
		return false, errors.New("RELAY_REPLICA_RECEIVER_CONTAINER_LISTEN must be true or false")
	}
	if flag == "false" {
		return false, nil
	}
	if organizerMode != "true" || city == "" || service == "" || destination == "" {
		return false, errors.New("replica receiver container listen requires organizer mode and the complete receiver scope")
	}
	return true, nil
}

func writePolicy(writers map[nostr.PubKey]bool) func(context.Context, nostr.Event) (bool, string) {
	return func(ctx context.Context, event nostr.Event) (bool, string) {
		if !writers[event.PubKey] {
			return true, "restricted: author is not an approved relay writer"
		}
		if !khatru.IsAuthed(ctx, event.PubKey) {
			return true, "auth-required: authenticate as the event author"
		}
		switch event.Kind {
		case 0, 5, 30301, 30302, 30303, 30304, 31923:
			return false, ""
		default:
			return true, "restricted: event kind is not enabled on this BitcoinWalk relay"
		}
	}
}

func newRelay(dbPath string, writers map[nostr.PubKey]bool) (*khatru.Relay, *boltdb.BoltBackend, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, nil, err
	}
	db := &boltdb.BoltBackend{Path: dbPath}
	if err := db.Init(); err != nil {
		return nil, nil, err
	}
	relay := khatru.NewRelay()
	relay.Info.Name = env("RELAY_NAME", "BitcoinWalk staging relay")
	relay.Info.Description = env("RELAY_DESCRIPTION", "BitcoinWalk Khatru foundation. Public reads; approved writers only.")
	admin := nostr.MustPubKeyFromHex(adminHex)
	relay.Info.PubKey = &admin
	relay.Info.Version = "bitcoinwalk-foundation-0.1.0"
	relay.MaxMessageSize = 65536
	relay.MaxAuthenticatedClients = 2
	relay.UseEventstore(db, 200)
	// Public queries intentionally have no NIP-42 requirement.
	relay.OnRequest = policies.RequestRejectionStrictDefaults
	relay.OnCount = policies.RequestRejectionStrictDefaults
	relay.OnEvent = policies.SeqEvent(writePolicy(writers), policies.EventRejectionStrictDefaults)
	relay.RejectConnection = policies.ConnectionRejectionStrictDefaults
	relay.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.DB.View(func(tx *bbolt.Tx) error { return nil }); err != nil {
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	relay.Router().HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, env("RELAY_LANDING_TEXT", "BitcoinWalk Khatru staging relay. Management UI is not yet implemented."))
	})
	return relay, db, nil
}

func run() error {
	if os.Getenv("RELAY_CITY_DIRECTORY_SUCCESSOR_REHEARSAL") != "" {
		return runCityDirectorySuccessorRehearsal()
	}
	if cityDirectoryAuditConfigured() {
		return runCityDirectoryAudit()
	}
	if replicaStagingEntitlementIssueConfigured() {
		return runReplicaStagingEntitlementIssue()
	}
	if replicaEntitlementApplyConfigured() {
		return runReplicaEntitlementApply()
	}
	if replicaEntitlementPlanConfigured() {
		return runReplicaEntitlementPlan()
	}
	if os.Getenv("RELAY_REPLICA_REGISTRY_ADD_CURRENT") != "" {
		return runReplicaRegistryAdd()
	}
	compactSource := os.Getenv("RELAY_REPLICA_DB_COMPACT_SOURCE")
	compactDestination := os.Getenv("RELAY_REPLICA_DB_COMPACT_DESTINATION")
	if compactSource != "" || compactDestination != "" {
		return runReplicaDBCompact(compactSource, compactDestination)
	}
	if path := os.Getenv("RELAY_REPLICA_DB_DIGEST"); path != "" {
		return runReplicaDBDigest(path)
	}
	if os.Getenv("RELAY_REPLICA_POLICY_AUDIT_EVENT") != "" {
		return runReplicaPolicyAudit()
	}
	if os.Getenv("RELAY_REPLICA_SHADOW_CITY") != "" {
		return runReplicaShadowBackfill()
	}
	if os.Getenv("RELAY_REPLICA_REPLAY_EVENT") != "" {
		return runReplicaReplay()
	}
	if os.Getenv("RELAY_REPLICA_ALERT_REHEARSAL_EVENT") != "" {
		return runReplicaAlertRehearsalArm()
	}
	if path := os.Getenv("RELAY_REPLICA_KEY_INIT"); path != "" {
		pubkey, err := initializeReplicaServiceKey(path)
		if err != nil {
			return err
		}
		fmt.Println(pubkey.Hex())
		return nil
	}
	if os.Getenv("RELAY_SIGNER_MODE") == "staging" {
		return runSignerStaging()
	}
	if path := os.Getenv("RELAY_CHAT_KEY_INIT"); path != "" {
		return createRelayCredential(path)
	}
	if os.Getenv("RELAY_CHAT_BACKUP") != "" {
		return backupChat()
	}
	if mode := env("RELAY_CHAT_MODE", ""); mode == "staging" || mode == "production" {
		return runChat()
	}
	if env("RELAY_CHAT_CHOOSER", "false") == "true" {
		return runChooserPreview()
	}
	if env("RELAY_CHAT_PILOT", "false") == "true" {
		return runChatPilot()
	}
	if replicaEntitlementRuntimeConfigured() && (env("RELAY_ORGANIZER_MODE", "false") != "true" || os.Getenv("RELAY_REPLICA_JOURNAL") == "" || os.Getenv("RELAY_REPLICA_REGISTRY") == "") {
		return errors.New("entitlement-required replication requires organizer mode, journal and registry")
	}
	reconciliationAudit := env("RELAY_REPLICA_RECONCILIATION_AUDIT", "false")
	if reconciliationAudit != "false" && reconciliationAudit != "true" {
		return errors.New("RELAY_REPLICA_RECONCILIATION_AUDIT must be true or false")
	}
	if reconciliationAudit == "true" && (env("RELAY_ORGANIZER_MODE", "false") != "true" || os.Getenv("RELAY_REPLICA_JOURNAL") == "" || os.Getenv("RELAY_REPLICA_REGISTRY") == "") {
		return errors.New("replica reconciliation audit requires organizer mode, journal and registry")
	}
	directoryTransportMode := env("RELAY_CITY_DIRECTORY_TRANSPORT", "")
	containerListen := env("RELAY_CITY_DIRECTORY_CONTAINER_LISTEN", "false")
	if containerListen != "false" && containerListen != "true" {
		return errors.New("RELAY_CITY_DIRECTORY_CONTAINER_LISTEN must be true or false")
	}
	if containerListen == "true" && directoryTransportMode == "" {
		return errors.New("container listen is restricted to the city directory transport")
	}
	receiverCity := os.Getenv("RELAY_REPLICA_RECEIVER_CITY")
	receiverService := os.Getenv("RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY")
	receiverDestination := os.Getenv("RELAY_REPLICA_RECEIVER_DESTINATION")
	receiverContainerListen, err := replicaReceiverContainerListenAllowed(env("RELAY_REPLICA_RECEIVER_CONTAINER_LISTEN", "false"), env("RELAY_ORGANIZER_MODE", "false"), receiverCity, receiverService, receiverDestination)
	if err != nil {
		return err
	}
	if containerListen == "true" && receiverContainerListen {
		return errors.New("container listen cannot enable directory and replica receiver modes together")
	}
	listen := env("RELAY_LISTEN", "127.0.0.1:3334")
	if err := validateListenAddress(listen, containerListen == "true" || receiverContainerListen); err != nil {
		return err
	}
	writers, err := parseWriters(env("RELAY_WRITERS", adminHex))
	if err != nil {
		return err
	}
	relay, db, err := newRelay(env("RELAY_DB", "./data/events.db"), writers)
	if err != nil {
		return err
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	if directoryTransportMode != "" {
		if env("RELAY_ORGANIZER_MODE", "false") != "false" {
			return errors.New("city directory transport cannot enable organizer mode")
		}
		anchorPath := os.Getenv("RELAY_CITY_DIRECTORY_TRANSPORT_ANCHORS")
		bundlePath := os.Getenv("RELAY_CITY_DIRECTORY_TRANSPORT_BUNDLE")
		cityID := os.Getenv("RELAY_CITY_DIRECTORY_TRANSPORT_CITY")
		if anchorPath == "" || bundlePath == "" || cityID == "" {
			return errors.New("city directory transport requires anchors, bundle and city")
		}
		state, err := configureCityDirectoryTransport(relay, db, anchorPath, bundlePath, cityID, directoryTransportMode)
		if err != nil {
			return fmt.Errorf("configure city directory transport: %w", err)
		}
		log.Printf("City directory %s transport enabled for %s at sequence %d", directoryTransportMode, cityID, state.Sequence)
	}
	var journal *replicaJournal
	var organizer *organizerPolicy
	var deliveryTransport replicaTransport
	if env("RELAY_ORGANIZER_MODE", "false") == "true" {
		organizer = enableOrganizers(relay, db, nostr.MustPubKeyFromHex(adminHex))
		log.Print("Organizer mode enabled; city approvals and editor lists require the super-admin")
		journalPath := os.Getenv("RELAY_REPLICA_JOURNAL")
		registryPath := os.Getenv("RELAY_REPLICA_REGISTRY")
		if journalPath != "" || registryPath != "" {
			if journalPath == "" || registryPath == "" {
				return errors.New("RELAY_REPLICA_JOURNAL and RELAY_REPLICA_REGISTRY must be configured together")
			}
			registry, err := loadReplicaRegistry(registryPath)
			if err != nil {
				return fmt.Errorf("load replica registry: %w", err)
			}
			requireEntitlements := env("RELAY_REPLICA_REQUIRE_ENTITLEMENTS", "false")
			entitlementLedgerPath := os.Getenv("RELAY_REPLICA_ENTITLEMENT_LEDGER")
			entitlementAuthorityHex := os.Getenv("RELAY_REPLICA_ENTITLEMENT_AUTHORITY")
			if requireEntitlements != "false" || entitlementLedgerPath != "" || entitlementAuthorityHex != "" {
				if requireEntitlements != "true" || entitlementLedgerPath == "" || entitlementAuthorityHex == "" {
					return errors.New("entitlement-required replication needs the exact flag, ledger and authority")
				}
				authority, err := nostr.PubKeyFromHex(entitlementAuthorityHex)
				if err != nil {
					return errors.New("invalid replica entitlement authority")
				}
				ledger, err := loadReplicaEntitlementLedger(entitlementLedgerPath, authority, time.Now())
				if err != nil {
					return fmt.Errorf("load replica entitlement ledger: %w", err)
				}
				if err := validateEntitledReplicaRegistry(registry, ledger); err != nil {
					return fmt.Errorf("validate replica entitlements: %w", err)
				}
			}
			journal, err = openReplicaJournal(journalPath, registry, nil)
			if err != nil {
				return fmt.Errorf("open replica journal: %w", err)
			}
			if reconciliationAudit == "true" {
				report, auditErr := auditReplicaReconciliation(organizer, journal)
				closeErr := journal.Close()
				if auditErr != nil {
					return fmt.Errorf("audit replica reconciliation: %w", auditErr)
				}
				if closeErr != nil {
					return fmt.Errorf("close replica journal after audit: %w", closeErr)
				}
				return json.NewEncoder(os.Stdout).Encode(report)
			}
			recovered, err := organizer.recoverReplicaJournal(journal)
			if err != nil {
				journal.Close()
				return fmt.Errorf("reconcile replica journal: %w", err)
			}
			attachReplicaJournal(organizer, journal)
			log.Printf("Replica acceptance journal enabled for %d operator-configured paid city relay(s); recovered %d missing record(s)", len(registry.destinations), recovered)
		}
		if receiverCity != "" || receiverService != "" || receiverDestination != "" {
			if receiverCity == "" || receiverService == "" || receiverDestination == "" {
				return errors.New("RELAY_REPLICA_RECEIVER_CITY, RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY and RELAY_REPLICA_RECEIVER_DESTINATION must be configured together")
			}
			serviceKey, err := nostr.PubKeyFromHex(receiverService)
			if err != nil {
				return errors.New("invalid replica receiver service public key")
			}
			receiver, err := newReplicaReceiver(organizer, replicaScope{CityID: receiverCity, ServiceKey: serviceKey, Destination: receiverDestination})
			if err != nil {
				return fmt.Errorf("configure replica receiver: %w", err)
			}
			if err := attachReplicaReceiverTransport(relay, receiver); err != nil {
				return fmt.Errorf("attach replica receiver transport: %w", err)
			}
			log.Printf("Replica WSS receiver enabled for one operator-configured city; service key %s", serviceKey.Hex())
		}
	}
	if keyPath := os.Getenv("RELAY_REPLICA_DELIVERY_KEY_FILE"); keyPath != "" {
		if organizer == nil || journal == nil {
			return errors.New("replica delivery requires organizer mode and the replica journal/registry")
		}
		serviceKey, err := loadReplicaServiceKey(keyPath)
		if err != nil {
			return fmt.Errorf("load replica delivery key: %w", err)
		}
		if nostr.GetPublicKey(serviceKey) == organizer.admin {
			return errors.New("replica delivery key must not be the super-admin key")
		}
		deliveryTransport = newReplicaWebSocketTransport(serviceKey)
		log.Printf("Replica WSS delivery worker configured with service key %s", nostr.GetPublicKey(serviceKey).Hex())
	}
	if tokenPath := os.Getenv("RELAY_REPLICA_STATUS_TOKEN_FILE"); tokenPath != "" {
		if journal == nil {
			return errors.New("replica status requires the replica journal/registry")
		}
		token, err := readReplicaStatusToken(tokenPath)
		if err != nil {
			return fmt.Errorf("load replica status token: %w", err)
		}
		relay.Router().HandleFunc("GET /replication/status", replicaStatusHandler(journal, token))
		log.Print("Authenticated content-free replica status enabled")
	}
	if journal != nil {
		defer journal.Close()
	}
	server := &http.Server{Addr: listen, Handler: relay, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if deliveryTransport != nil {
		go runReplicaDeliveryWorker(ctx, organizer, journal, deliveryTransport)
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("BitcoinWalk relay listening on %s; %d permitted writer(s)", listen, len(writers))
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
