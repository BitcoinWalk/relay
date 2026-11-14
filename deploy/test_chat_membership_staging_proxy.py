import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("installer", Path(__file__).with_name("install-chat-membership-staging-proxy.py"))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class ProxyPatchTests(unittest.TestCase):
    def test_only_staging_changes(self):
        route = "\t\thandle /api/chat/upload-authorization {\n\t\t\treverse_proxy 127.0.0.1:3335\n\t\t}\n"
        template = Path(__file__).with_name("Caddyfile.external-chat").read_text()
        old = template.replace(route, "")
        before = "production.example {\n respond 200\n}\n" + old
        after = installer.candidate(before)
        self.assertEqual(after, "production.example {\n respond 200\n}\n" + template)
        with self.assertRaises(ValueError):
            installer.candidate(after)

    def test_changed_or_missing_site_refused(self):
        for text in ["", "chat-staging.bitcoinwalk.org {\n respond 200\n}\n"]:
            with self.assertRaises(ValueError):
                installer.candidate(text)


if __name__ == "__main__":
    unittest.main()
