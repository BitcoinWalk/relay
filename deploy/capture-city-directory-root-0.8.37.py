#!/usr/bin/env python3
"""Capture one exact kind-30309 event using only the Python standard library."""

import argparse
import base64
import hashlib
import json
import os
import socket
import ssl
import struct
import sys
from urllib.parse import urlsplit

DIRECTORY_KIND = 30309
MAX_MESSAGE = 65536
ROOT_ID = "d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79"


def parse_relay_url(raw):
    parsed = urlsplit(raw)
    loopback = parsed.scheme == "ws" and parsed.hostname in {"127.0.0.1", "::1"}
    if parsed.scheme != "wss" and not loopback:
        raise ValueError("relay must be wss or loopback ws")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("relay URL must not contain credentials, query or fragment")
    if not parsed.hostname or parsed.path not in {"", "/"}:
        raise ValueError("relay must be a root URL")
    return parsed


def client_frame(payload, opcode=0x1):
    if isinstance(payload, str):
        payload = payload.encode("utf-8")
    mask = os.urandom(4)
    length = len(payload)
    if length < 126:
        header = bytes((0x80 | opcode, 0x80 | length))
    elif length <= 65535:
        header = bytes((0x80 | opcode, 0xFE)) + struct.pack("!H", length)
    else:
        raise ValueError("client message is too large")
    masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
    return header + mask + masked


def read_exact(stream, length):
    output = bytearray()
    while len(output) < length:
        chunk = stream.recv(length - len(output))
        if not chunk:
            raise RuntimeError("relay closed the connection")
        output.extend(chunk)
    return bytes(output)


def read_frame(stream):
    first, second = read_exact(stream, 2)
    if first & 0x70:
        raise RuntimeError("reserved relay frame")
    final = bool(first & 0x80)
    opcode = first & 0x0F
    masked = bool(second & 0x80)
    length = second & 0x7F
    if length == 126:
        length = struct.unpack("!H", read_exact(stream, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", read_exact(stream, 8))[0]
    if masked or length > MAX_MESSAGE:
        raise RuntimeError("invalid relay frame")
    return final, opcode, read_exact(stream, length)


def websocket_stream(parsed, timeout):
    port = parsed.port or (443 if parsed.scheme == "wss" else 80)
    stream = socket.create_connection((parsed.hostname, port), timeout=timeout)
    if parsed.scheme == "wss":
        stream = ssl.create_default_context().wrap_socket(stream, server_hostname=parsed.hostname)
    stream.settimeout(timeout)
    key = base64.b64encode(os.urandom(16)).decode("ascii")
    host = parsed.hostname if parsed.port is None else f"{parsed.hostname}:{parsed.port}"
    request = (
        f"GET / HTTP/1.1\r\nHost: {host}\r\nUpgrade: websocket\r\n"
        f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n\r\n"
    )
    stream.sendall(request.encode("ascii"))
    response = bytearray()
    while b"\r\n\r\n" not in response:
        response.extend(read_exact(stream, 1))
        if len(response) > 16384:
            raise RuntimeError("oversized WebSocket handshake")
    lines = response.decode("iso-8859-1").split("\r\n")
    headers = {}
    for line in lines[1:]:
        if ":" in line:
            name, value = line.split(":", 1)
            headers[name.strip().lower()] = value.strip()
    expected = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("ascii")).digest()).decode("ascii")
    if not lines[0].startswith("HTTP/1.1 101 ") or headers.get("sec-websocket-accept") != expected:
        raise RuntimeError("WebSocket handshake was rejected")
    return stream


def capture(relay, event_id, timeout):
    if len(event_id) != 64 or any(value not in "0123456789abcdef" for value in event_id):
        raise ValueError("event ID must be 32-byte lowercase hex")
    parsed = parse_relay_url(relay)
    subscription = "bitcoinwalk-directory-" + event_id[:16]
    stream = websocket_stream(parsed, timeout)
    try:
        request = json.dumps(["REQ", subscription, {"ids": [event_id], "kinds": [DIRECTORY_KIND], "limit": 1}], separators=(",", ":"))
        stream.sendall(client_frame(request))
        message_opcode = None
        fragments = bytearray()
        while True:
            final, opcode, payload = read_frame(stream)
            if opcode == 0x8:
                raise RuntimeError("relay closed before returning the directory root")
            if opcode == 0x9:
                stream.sendall(client_frame(payload, opcode=0xA))
                continue
            if opcode == 0x1:
                if message_opcode is not None:
                    raise RuntimeError("overlapping relay messages")
                message_opcode = opcode
                fragments.extend(payload)
            elif opcode == 0x0:
                if message_opcode is None:
                    raise RuntimeError("unexpected relay continuation")
                fragments.extend(payload)
            else:
                raise RuntimeError("unexpected relay frame")
            if len(fragments) > MAX_MESSAGE:
                raise RuntimeError("relay message is too large")
            if not final:
                continue
            if message_opcode != 0x1:
                raise RuntimeError("unexpected relay message type")
            message = json.loads(bytes(fragments).decode("utf-8"))
            message_opcode = None
            fragments.clear()
            if not isinstance(message, list) or len(message) < 2:
                raise RuntimeError("invalid relay message")
            if message[0] == "EVENT" and message[1] == subscription:
                event = message[2]
                if event.get("id") != event_id or event.get("kind") != DIRECTORY_KIND:
                    raise RuntimeError("relay returned an unexpected directory event")
                return {"version": 1, "events": [event]}
            if message[0] == "EOSE" and message[1] == subscription:
                raise RuntimeError("directory root was not found")
            if message[0] == "CLOSED" and message[1] == subscription:
                raise RuntimeError("relay closed the directory subscription")
    finally:
        stream.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("relay")
    parser.add_argument("event_id", nargs="?", default=ROOT_ID)
    parser.add_argument("--timeout", type=float, default=15.0)
    arguments = parser.parse_args()
    print(json.dumps(capture(arguments.relay, arguments.event_id, arguments.timeout), separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
