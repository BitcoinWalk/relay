import importlib.util
import io
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).with_name("capture-city-directory-root-0.8.37.py")
SPEC = importlib.util.spec_from_file_location("directory_capture", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class DirectoryCaptureTests(unittest.TestCase):
    def test_accepts_public_tls_root(self):
        parsed = MODULE.parse_relay_url("wss://directory-2-staging.bitcoinwalk.org/")
        self.assertEqual(parsed.hostname, "directory-2-staging.bitcoinwalk.org")

    def test_accepts_loopback_plaintext(self):
        parsed = MODULE.parse_relay_url("ws://127.0.0.1:3343/")
        self.assertEqual(parsed.port, 3343)

    def test_rejects_non_loopback_plaintext(self):
        with self.assertRaisesRegex(ValueError, "loopback"):
            MODULE.parse_relay_url("ws://example.com/")

    def test_rejects_credentialed_url(self):
        with self.assertRaisesRegex(ValueError, "credentials"):
            MODULE.parse_relay_url("wss://user:secret@example.com/")

    def test_client_frame_is_masked(self):
        frame = MODULE.client_frame("hello")
        self.assertEqual(frame[0], 0x81)
        self.assertTrue(frame[1] & 0x80)
        self.assertNotEqual(frame[-5:], b"hello")

    def test_reads_non_final_text_fragment(self):
        class Stream(io.BytesIO):
            recv = io.BytesIO.read

        final, opcode, payload = MODULE.read_frame(Stream(b"\x01\x05hello"))
        self.assertFalse(final)
        self.assertEqual(opcode, 0x1)
        self.assertEqual(payload, b"hello")


if __name__ == "__main__":
    unittest.main()
