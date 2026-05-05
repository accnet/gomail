#!/usr/bin/env python3
import pathlib
import socket
import threading
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT_DIR = ROOT / ".run" / "smtp-sink"
OUT_DIR.mkdir(parents=True, exist_ok=True)


def send(conn, line):
    conn.sendall((line + "\r\n").encode())


def handle(conn, addr):
    with conn:
        send(conn, "220 dev-smtp-sink.local ESMTP")
        data_mode = False
        chunks = []
        while True:
            line = b""
            while not line.endswith(b"\n"):
                part = conn.recv(1)
                if not part:
                    return
                line += part
            text = line.decode(errors="replace")
            cmd = text.strip().upper()
            if data_mode:
                if text.rstrip("\r\n") == ".":
                    stamp = time.strftime("%Y%m%d-%H%M%S")
                    path = OUT_DIR / f"{stamp}-{threading.get_ident()}.eml"
                    path.write_text("".join(chunks))
                    send(conn, "250 OK")
                    data_mode = False
                    chunks = []
                else:
                    chunks.append(text)
                continue
            if cmd.startswith("EHLO"):
                send(conn, "250-dev-smtp-sink.local")
                send(conn, "250 OK")
            elif cmd.startswith("HELO") or cmd.startswith("MAIL FROM:") or cmd.startswith("RCPT TO:"):
                send(conn, "250 OK")
            elif cmd == "DATA":
                send(conn, "354 End data with <CR><LF>.<CR><LF>")
                data_mode = True
            elif cmd == "QUIT":
                send(conn, "221 Bye")
                return
            else:
                send(conn, "250 OK")


def main():
    sock = socket.socket()
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("127.0.0.1", 2526))
    sock.listen(20)
    print("dev SMTP sink listening on 127.0.0.1:2526", flush=True)
    while True:
        conn, addr = sock.accept()
        threading.Thread(target=handle, args=(conn, addr), daemon=True).start()


if __name__ == "__main__":
    main()
