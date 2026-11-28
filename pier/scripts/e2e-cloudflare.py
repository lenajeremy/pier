# A stand-in for Cloudflare's API, for the e2e suite: buckets are folders
# that an rclone S3 server serves, and keys come from a list rclone knows.
import json, os, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
DATA = sys.argv[1]
ACCT = '0123456789abcdef0123456789abcdef'
keys = [('key%d' % i, 'value-%d' % i) for i in range(1, 41)]
made = 0
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def send(self, code, result=None, errors=None):
        body = json.dumps({'success': code < 300, 'errors': errors or [], 'result': result}).encode()
        self.send_response(code); self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(body))); self.end_headers(); self.wfile.write(body)
    def body(self):
        n = int(self.headers.get('Content-Length') or 0)
        return json.loads(self.rfile.read(n) or b'{}')
    def auth(self):
        if self.headers.get('Authorization') != 'Bearer admin-token':
            self.send(403, None, [{'code': 9109, 'message': 'Invalid access token'}]); return False
        return True
    def do_GET(self):
        if not self.auth(): return
        p = self.path.split('?')[0]
        if p.endswith('/tokens/verify'): return self.send(200, {'id': 'adminkey', 'status': 'active'})
        if p.endswith('/tokens/permission_groups'): return self.send(200, [{'id': 'g1', 'name': 'Workers R2 Storage Bucket Item Write'}])
        if p == '/zones': return self.send(200, [])
        self.send(404, None, [{'code': 7003, 'message': 'no route'}])
    def do_POST(self):
        global made
        if not self.auth(): return
        p = self.path.split('?')[0]; b = self.body()
        if p == f'/accounts/{ACCT}/r2/buckets':
            os.makedirs(os.path.join(DATA, b['name']), exist_ok=True); return self.send(200, {'name': b['name']})
        if p == f'/accounts/{ACCT}/tokens':
            k = keys[made]; made += 1; return self.send(200, {'id': k[0], 'value': k[1]})
        if p.endswith('/domains/custom'): return self.send(200, b)
        self.send(404, None, [{'code': 7003, 'message': 'no route'}])
    def do_PUT(self):
        if not self.auth(): return
        p = self.path.split('?')[0]
        if p.endswith('/domains/managed'):
            name = p.split('/r2/buckets/')[1].split('/')[0]
            return self.send(200, {'bucketId': name, 'domain': 'pub-%s.r2.dev' % name, 'enabled': True})
        self.send(404, None, [{'code': 7003, 'message': 'no route'}])
    def do_DELETE(self):
        if not self.auth(): return
        p = self.path.split('?')[0]
        if '/r2/buckets/' in p and '/domains/' not in p:
            d = os.path.join(DATA, p.split('/r2/buckets/')[1])
            if os.path.isdir(d):
                if any(os.scandir(d)): return self.send(409, None, [{'code': 10008, 'message': 'The bucket you tried to delete is not empty'}])
                os.rmdir(d)
            return self.send(200, {})
        return self.send(200, {})
ThreadingHTTPServer(('127.0.0.1', int(sys.argv[2])), H).serve_forever()
