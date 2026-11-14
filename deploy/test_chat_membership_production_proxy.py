import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("installer", Path(__file__).with_name("install-chat-membership-production-proxy.py"))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class ProductionProxyTests(unittest.TestCase):
    def test_exact_route_only(self):
        other = "chat-staging.bitcoinwalk.org {\n respond 200\n}\n"
        health = "\t\thandle /healthz {\n\t\t\treverse_proxy 127.0.0.1:3339\n\t\t}"
        target = "chat.bitcoinwalk.org {\n\troute {\n" + health + "\n\t}\n}\n"
        route = "\t\thandle /api/chat/upload-authorization {\n\t\t\treverse_proxy 127.0.0.1:3339\n\t\t}"
        before = other + target + "unrelated.example {\n respond 200\n}\n"
        after = installer.candidate(before)
        self.assertEqual(after, before.replace(health, health + "\n" + route))
        with self.assertRaises(ValueError):
            installer.candidate(after)
        with self.assertRaises(ValueError):
            installer.candidate(target + target)

    def test_missing_or_wrong_listener_refused(self):
        for text in ["", "chat.bitcoinwalk.org {\n respond 200\n}\n", "chat.bitcoinwalk.org {\n\t\thandle /healthz {\n\t\t\treverse_proxy 127.0.0.1:3335\n\t\t}\n}\n"]:
            with self.assertRaises(ValueError):
                installer.candidate(text)


if __name__ == "__main__":
    unittest.main()
