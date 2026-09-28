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


if __name__ == "__main__":
    unittest.main()
