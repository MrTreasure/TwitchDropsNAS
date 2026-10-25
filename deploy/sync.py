"""Native scheduler entry; no credentials or account data are printed."""
import http.client
import os
import socket

class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(os.environ.get('TDM_SOCKET', '/run/twitch-drops/service.sock'))

connection = UnixHTTP('localhost', timeout=90)
connection.request('POST', '/internal/scan', body=b'{}', headers={'Content-Type': 'application/json'})
response = connection.getresponse()
response.read()
if response.status != 200:
    raise SystemExit('Official state check failed; inspect service status.')
print('Official state check finished; inspect source timestamps and capabilities.')
