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
        installer = (DEPLOY / "fix-replica-candidate-tmpfs-0.8.45.sh").read_text()
        backup = installer.index('cp -p "$state" "$backup/events.db"')
        recreate = installer.index('up -d --no-build --force-recreate', backup)
        self.assertLess(backup, recreate)
        self.assertIn('install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target"', installer)


if __name__ == "__main__":
    unittest.main()
