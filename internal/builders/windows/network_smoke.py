import socket

print("socket-import-ok", flush=True)

import select
import threading

print("socket-support-imports-ok", flush=True)


def join_thread(thread, name):
    thread.join(10)
    assert not thread.is_alive(), f"{name} thread did not finish"


addresses = socket.getaddrinfo("localhost", 80)
assert addresses, "getaddrinfo returned no addresses"
print("getaddrinfo-ok", flush=True)

listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
listener.settimeout(10)
listener.bind(("127.0.0.1", 0))
listener.listen(1)
echo_errors = []


def echo_server():
    try:
        connection, _ = listener.accept()
        with connection:
            connection.settimeout(10)
            assert connection.recv(4) == b"ping"
            connection.sendall(b"pong")
    except BaseException as exc:
        echo_errors.append(exc)


echo_thread = threading.Thread(target=echo_server)
echo_thread.start()
with socket.create_connection(("localhost", listener.getsockname()[1]), timeout=10) as client:
    client.sendall(b"ping")
    readable, writable, exceptional = select.select([client], [client], [client], 10)
    assert client in readable and client in writable and not exceptional
    assert client.recv(4) == b"pong"
listener.close()
join_thread(echo_thread, "echo")
if echo_errors:
    raise echo_errors[0]
print("tcp-echo-select-ok", flush=True)

left, right = socket.socketpair()
try:
    left.settimeout(10)
    right.settimeout(10)
    left.sendall(b"pair")
    readable, _, _ = select.select([right], [], [], 10)
    assert readable == [right]
    assert right.recv(4) == b"pair"
finally:
    left.close()
    right.close()
print("socketpair-ok", flush=True)

import asyncio

asyncio.run(asyncio.sleep(0))
print("asyncio-ok", flush=True)

import http.client
import http.server

print("http-imports-ok", flush=True)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"http-ok"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
server.timeout = 10
http_errors = []


def http_server():
    try:
        server.handle_request()
    except BaseException as exc:
        http_errors.append(exc)


http_thread = threading.Thread(target=http_server)
http_thread.start()
connection = http.client.HTTPConnection("localhost", server.server_port, timeout=10)
try:
    connection.request("GET", "/")
    response = connection.getresponse()
    assert response.status == 200
    assert response.read() == b"http-ok"
finally:
    connection.close()
    server.server_close()
join_thread(http_thread, "http")
if http_errors:
    raise http_errors[0]

print("windows-network-ok")
