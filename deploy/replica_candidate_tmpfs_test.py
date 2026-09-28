import pathlib
import unittest


DEPLOY = pathlib.Path(__file__).parent


class ReplicaCandidateTmpfsTests(unittest.TestCase):
    def test_candidate_keeps_private_read_only_topology(self):
        compose = (DEPLOY / "compose.replica-candidate-0.8.45.yaml").read_text()
        self.assertIn('read_only: true', compose)
        self.assertIn('user: "65532:65532"', compose)
        self.assertIn('name: root_my_custom_network', compose)
        self.assertIn('expose:\n      - "3344"', compose)
        self.assertNotIn('ports:', compose)

    def test_candidate_tmpfs_is_bounded_and_hardened(self):
        compose = (DEPLOY / "compose.replica-candidate-0.8.45.yaml").read_text()
        self.assertIn('/tmp:rw,noexec,nosuid,nodev,size=32m,mode=0700,uid=65532,gid=65532', compose)

    def test_installer_backs_up_database_before_recreation(self):
        installer = (DEPLOY / "resume-replica-candidate-tmpfs-0.8.46.sh").read_text()
        backup = installer.index('cp -p "$state" "$backup/events.db"')
        recreate = installer.index('up -d --no-build --force-recreate', backup)
        self.assertLess(backup, recreate)
        self.assertIn('install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target"', installer)

    def test_verifier_uses_authoritative_hostconfig_tmpfs(self):
        installer = (DEPLOY / "resume-replica-candidate-tmpfs-0.8.46.sh").read_text()
        self.assertIn('container["HostConfig"].get("Tmpfs", {})', installer)
        self.assertNotIn('mount for mount in container["Mounts"]', installer)

    def test_candidate_binary_embeds_timezone_database(self):
        source = (DEPLOY.parent / "main.go").read_text()
        self.assertIn('_ "time/tzdata"', source)
        self.assertIn('RELAY_TIMEZONE_PROBE', source)

    def test_timezone_upgrade_preserves_hardened_runtime(self):
        compose = (DEPLOY / "compose.replica-candidate-0.8.47.yaml").read_text()
        self.assertIn('image: bitcoinwalk-replica-candidate:0.8.47', compose)
        self.assertIn('read_only: true', compose)
        self.assertIn('/tmp:rw,noexec,nosuid,nodev,size=32m,mode=0700,uid=65532,gid=65532', compose)
        self.assertNotIn('ports:', compose)

    def test_timezone_upgrade_probes_valid_and_invalid_zones_in_scratch_image(self):
        installer = (DEPLOY / "upgrade-replica-candidate-timezone-0.8.47.sh").read_text()
        self.assertIn('RELAY_TIMEZONE_PROBE=America/Chicago', installer)
        self.assertIn('RELAY_TIMEZONE_PROBE=Not/A_Real_Zone', installer)
        self.assertIn('cp -p "$state" "$backup/events.db"', installer)

    def test_replacement_candidate_keeps_hardened_runtime(self):
        compose = (DEPLOY / "compose.replica-candidate-0.8.48.yaml").read_text()
        self.assertIn('image: bitcoinwalk-replica-candidate:0.8.48', compose)
        self.assertIn('read_only: true', compose)
        self.assertIn('/tmp:rw,noexec,nosuid,nodev,size=32m,mode=0700,uid=65532,gid=65532', compose)
        self.assertNotIn('ports:', compose)

    def test_replacement_restore_is_backup_first_and_requires_empty_readback(self):
        installer = (DEPLOY / "restore-replica-candidate-replacement-0.8.48.sh").read_text()
        dirty_backup = installer.index('cp -p "$state" "$backup/events.dirty.db"')
        restore = installer.index('install -o 65532 -g 65532 -m 0600 "$restore/events.db" "$state"')
        self.assertLess(dirty_backup, restore)
        self.assertIn('report["source"]["occurrenceIds"]', installer)
        self.assertIn('restored candidate is not empty', installer)

    def test_visible_state_reset_is_backup_first_and_preserves_hardening(self):
        installer = (DEPLOY / "reset-replica-candidate-visible-0.8.49.sh").read_text()
        backup = installer.index('cp -p "$state" "$backup/events.divergent.db"')
        restore = installer.index('install -o 65532 -g 65532 -m 0600 "$restore/events.db" "$state"')
        self.assertLess(backup, restore)
        self.assertIn('ReadonlyRootfs', installer)
        self.assertIn('PortBindings', installer)
        self.assertIn('reset candidate is not empty', installer)

    def test_visible_backfill_uses_exact_accepted_public_snapshot(self):
        installer = (DEPLOY / "backfill-replica-candidate-visible-0.8.49.sh").read_text()
        self.assertIn('>"$backup/public-baseline.json"', installer)
        self.assertIn('RELAY_REPLICA_SHADOW_PUBLIC_SNAPSHOT="$backup/public-baseline.json"', installer)
        self.assertIn("grep -q '\"delivered\":7'", installer)
        self.assertNotIn("grep -q '\"delivered\":8'", installer)


if __name__ == "__main__":
    unittest.main()
