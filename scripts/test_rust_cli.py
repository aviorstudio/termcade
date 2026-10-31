#!/usr/bin/env python3
"""Independent, offline CLI journeys against a loopback marketplace.

The common cases also run against the original CLI during migration. No real
credentials, browsers, GitHub releases or marketplace writes are used.
"""
import argparse
import contextlib
import datetime
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import threading
import unittest
import urllib.parse
import zipfile

ROOT = Path(__file__).resolve().parents[1]
CLI = None
WASM = None
REFERENCE = False
EXPECTED_VERSION = 'dev'
TOKEN = 'tcc_' + 'a' * 43
KEY = 'ak_' + 'b' * 32
NOW = datetime.datetime.now(datetime.timezone.utc)
EXPIRY = (NOW + datetime.timedelta(hours=1)).isoformat().replace('+00:00', 'Z')
MANIFEST = b'''[game]
id = "test/probe"
name = "Probe"
version = "0.1.0"
description = "An independent fixture"
[requirements]
abi = 1
width = 16
height = 8
[controls]
"Z" = "Add ten points"
'''

class State:
    def __init__(self, wasm):
        output = io.BytesIO()
        with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as archive:
            archive.writestr('termcade.toml', MANIFEST)
            archive.writestr('game.wasm', wasm)
        self.package = output.getvalue()
        self.game = dict(id='test/probe', name='Probe', version='0.1.0', abi=1,
                         width=16, height=8, has_package=True, description='A fixture',
                         repo='https://github.com/example/probe', likes=0, liked=False)
        self.resolved = dict(self.game, sha256=hashlib.sha256(self.package).hexdigest(),
                             size=len(self.package), tag='v0.1.0', asset='probe.tcade')
        self.requests = []
        self.library = []
        self.fail = {}
        self.pending = False
        self.device_started = threading.Event()
        self.username = 'player'
        self.keys = []
        self.orgs = []
        self.members = []
        self.activity = []

    def handle(self, method, path, body, auth):
        self.requests.append((method, path, body, auth))
        if (method, path) in self.fail:
            return self.fail[(method, path)]
        if path == '/v1/device/start':
            self.device_started.set()
            return 200, dict(device_code='tcd_' + 'd' * 43, user_code='ABCD-EFGH',
                             verification_uri='https://app.termca.de/pair',
                             expires_in=60, interval=1)
        if path == '/v1/device/poll':
            if self.pending:
                return 200, dict(status='pending', interval=1)
            return 200, dict(status='approved', token=TOKEN, credential_id='cli_fixture', expires_at=EXPIRY)
        if path == '/v1/me':
            if method == 'DELETE':
                return 204, None
            return 200, dict(email='player@example.test', username=self.username,
                             orgs=self.orgs, handles=[self.username])
        if path == '/v1/me/username':
            self.username = body['username']
            return 200, dict(name=self.username, is_org=False)
        if path.startswith('/v1/usernames/'):
            return 404, dict(message='this handle is available')
        if path == '/v1/games':
            return 200, dict(games=[self.game])
        if path == '/v1/library':
            return 200, self.library
        if path == '/v1/library/test/probe':
            self.library = [self.game] if method == 'PUT' else []
            return 204, None
        if path == '/v1/games/test/probe/resolve':
            return 200, self.resolved
        if path == '/v1/games/test/probe/download':
            return 200, self.package
        if path == '/v1/keys':
            if method == 'POST':
                key = dict(id='key_fixture', name=body['name'], username=body['username'],
                           token=KEY, created_at=NOW.isoformat())
                self.keys = [dict(key, token='')]
                return 201, key
            return 200, self.keys
        if path.startswith('/v1/keys/'):
            self.keys = []
            return 204, None
        if path == '/v1/orgs' and method == 'POST':
            org = dict(body, admin=True, games=[])
            self.orgs = [org]
            return 201, org
        if path == '/v1/orgs/studio/members':
            if method == 'POST':
                self.members = [body]
                return 204, None
            return 200, self.members
        if path.startswith('/v1/orgs/studio/members/'):
            self.members = []
            return 204, None
        if path == '/v1/orgs/studio':
            org = dict(username='studio', bio='bio', link='', admin=True, games=[])
            if method == 'DELETE':
                self.orgs = []
                return 204, None
            if method == 'PATCH':
                org.update(body)
            return 200, org
        if path == '/v1/publish':
            return 201, dict(self.resolved, repo=body['repo'], tag=body['tag'], asset=body['asset'])
        if path == '/v1/activity':
            return 200, self.activity
        if path.startswith('/v1/activity/'):
            return 200, dict(id='test/probe', personal_best=body['score'], plays=1)
        return 404, dict(message='fixture route missing')


@contextlib.contextmanager
def marketplace(state):
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'
        def log_message(self, *_):
            pass
        def dispatch(self):
            path = urllib.parse.urlsplit(self.path).path
            length = int(self.headers.get('Content-Length', 0))
            if length > 1 << 20:
                self.send_error(413)
                return
            raw = self.rfile.read(length)
            body = json.loads(raw) if raw else None
            code, result = state.handle(self.command, path, body, self.headers.get('Authorization', ''))
            encoded = result if isinstance(result, bytes) else json.dumps(result).encode() if result is not None else b''
            self.send_response(code)
            self.send_header('Content-Type', 'application/octet-stream' if isinstance(result, bytes) else 'application/json')
            self.send_header('Content-Length', str(len(encoded)))
            self.end_headers()
            with contextlib.suppress(BrokenPipeError, ConnectionResetError):
                self.wfile.write(encoded)
        do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = dispatch
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.daemon_threads = True
    worker = threading.Thread(target=server.serve_forever, kwargs={'poll_interval': 0.05}, daemon=True)
    worker.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}'
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)


class Journeys(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='termcade-cli-contract-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = State(WASM.read_bytes())
        self.server = marketplace(self.state)
        self.base = self.server.__enter__()
        self.addCleanup(self.server.__exit__, None, None, None)
        self.env = dict(os.environ, XDG_CONFIG_HOME=str(self.root/'config'),
                        XDG_DATA_HOME=str(self.root/'data'), TERMCADE_REGISTRY=self.base,
                        TERMCADE_DEV_APP_URL=self.base, TERMCADE_PIXELS='quad', NO_COLOR='1')
        self.env.pop('TERMCADE_TOKEN', None)
        self.session = self.root/'config/termcade/session.json'
        self.installed = self.root/'data/termcade/games/test/probe'

    def sign_in(self, token=TOKEN):
        self.session.parent.mkdir(parents=True, exist_ok=True)
        self.session.write_text(json.dumps(dict(registry=self.base, email='player@example.test',
                                              username='player', token=token, credential_id='cli_fixture')))
        self.session.chmod(0o600)

    def run_cli(self, *args, code=0, env=None, cwd=None, timeout=25):
        result = subprocess.run([str(CLI), *map(str,args)], cwd=cwd or self.root,
                                env=env or self.env, text=True, encoding='utf-8',
                                stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout)
        self.assertEqual(result.returncode, code, (args,result.returncode,result.stdout,result.stderr))
        return result

    def test_help_version_unknown_and_usage(self):
        self.assertEqual(self.run_cli('help').stdout, (ROOT/'src/usage.txt').read_text(encoding='utf-8'))
        self.assertEqual(self.run_cli('version').stdout, f'termcade {EXPECTED_VERSION}\n')
        result = self.run_cli('unknown-command',code=2)
        self.assertIn('unknown command "unknown-command"',result.stderr)
        for args in [('add',),('remove',),('login','bad'),('dev','install'),('dev','new'),('dev','build','a','b')]:
            self.assertIn('usage:',self.run_cli(*args,code=1).stderr)

    def test_marketplace_ids_and_pinning_are_rejected_before_authentication(self):
        for name, message in [('probe','missing an author'),('test/probe@v1','versions cannot be pinned'),('local.tcade','marketplace ids only'),('../probe','marketplace ids only')]:
            self.assertIn(message,self.run_cli('add',name,code=1).stderr)
        self.assertEqual(self.state.requests,[])

    def test_signed_out_commands_do_not_mutate_the_marketplace(self):
        for args in [('add','test/probe'),('sync',),('keys',),('org',),('whoami',),('account','delete','player')]:
            self.assertIn('termcade login',self.run_cli(*args,code=1).stderr)
        self.assertEqual(self.state.requests,[])

    def test_non_cli_saved_credentials_are_never_sent(self):
        self.sign_in(KEY)
        self.assertIn('unsupported credential',self.run_cli('whoami',code=1).stderr)
        self.assertEqual(self.state.requests,[])

    def test_device_login_verifies_the_credential_and_restores_the_library(self):
        result = self.run_cli('login',timeout=15)
        self.assertIn('https://app.termca.de/pair',result.stdout)
        self.assertIn('ABCD-EFGH',result.stdout)
        self.assertIn('logged in as player@example.test',result.stdout)
        self.assertNotIn('tcd_',result.stdout+result.stderr)
        self.assertNotIn(TOKEN,result.stdout+result.stderr)
        self.assertEqual(json.loads(self.session.read_text())['token'],TOKEN)
        if os.name == 'posix':
            self.assertEqual(self.session.stat().st_mode & 0o777,0o600)
        self.assertEqual(self.state.requests[0][3],'')
        self.assertEqual(self.state.requests[1][3],'')
        self.assertEqual(self.state.requests[2][3],'Bearer '+TOKEN)

    @unittest.skipUnless(os.name == 'posix','POSIX signal contract')
    def test_login_interrupt_does_not_save_a_partial_session(self):
        self.state.pending=True
        process=subprocess.Popen([str(CLI),'login'],cwd=self.root,env=self.env,stdin=subprocess.DEVNULL,
                                 stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        try:
            self.assertTrue(self.state.device_started.wait(8))
            process.send_signal(signal.SIGINT)
            output,error=process.communicate(timeout=8)
            self.assertEqual(process.returncode,1)
            self.assertIn('login canceled',error)
            self.assertFalse(self.session.exists())
            self.assertNotIn('tcd_',output+error)
        finally:
            if process.poll() is None:
                os.killpg(process.pid,signal.SIGKILL)
                process.wait(timeout=5)

    def test_library_is_recorded_before_download_and_package_install(self):
        self.sign_in()
        self.assertIn('added test/probe 0.1.0',self.run_cli('add','test/probe').stdout)
        self.assertTrue((self.installed/'game.wasm').is_file())
        paths=[(m,p)for m,p,_,_ in self.state.requests]
        self.assertEqual(paths[:3],[('PUT','/v1/library/test/probe'),('GET','/v1/games/test/probe/resolve'),('GET','/v1/games/test/probe/download')])
        self.assertTrue(all(a=='Bearer '+TOKEN for _,_,_,a in self.state.requests))

    def test_digest_mismatch_never_installs_but_library_choice_survives(self):
        self.sign_in();self.state.resolved['sha256']='0'*64
        self.assertIn('checksum mismatch',self.run_cli('add','test/probe',code=1).stderr)
        self.assertFalse(self.installed.exists());self.assertEqual(len(self.state.library),1)

    def test_sync_preserves_existing_local_bytes_and_restores_only_missing_games(self):
        self.sign_in();self.state.library=[self.state.game]
        self.run_cli('sync');self.assertTrue((self.installed/'game.wasm').exists())
        (self.installed/'game.wasm').write_bytes(b'owner local work')
        self.state.requests.clear()
        self.assertIn('already here',self.run_cli('sync').stdout)
        self.assertEqual((self.installed/'game.wasm').read_bytes(),b'owner local work')
        self.assertFalse(any(p.endswith('/download') for _,p,_,_ in self.state.requests))

    def test_failed_account_removal_keeps_the_local_package(self):
        self.sign_in();self.run_cli('add','test/probe')
        self.state.fail[('DELETE','/v1/library/test/probe')]=(503,dict(message='try later'))
        self.assertIn('try later',self.run_cli('remove','test/probe',code=1).stderr)
        self.assertTrue((self.installed/'game.wasm').exists())
        self.state.fail.clear();self.run_cli('remove','test/probe');self.assertFalse(self.installed.exists())

    def test_list_seeds_once_without_resurrecting_removed_starters(self):
        first=self.run_cli('list').stdout
        for name in ['ASTEROID','TETRIS','BRICKOUGH']:
            self.assertIn(name,first)
        starter=self.root/'data/termcade/games/aviorstudio/asteroid'
        self.run_cli('remove','aviorstudio/asteroid')
        self.assertFalse(starter.exists());self.assertNotIn('ASTEROID',self.run_cli('list').stdout)

    def test_whoami_publish_and_scoped_publish_keys(self):
        self.sign_in();self.assertIn('player@example.test',self.run_cli('whoami').stdout)
        self.assertIn('published test/probe',self.run_cli('publish','https://github.com/example/probe','v0.1.0','probe.tcade').stdout)
        keyed=dict(self.env,TERMCADE_TOKEN=KEY)
        self.run_cli('publish','https://github.com/example/probe','v0.1.0','probe.tcade',env=keyed)
        self.assertEqual(self.state.requests[-1][3],'Bearer '+KEY)
        count=len(self.state.requests)
        self.assertIn('scoped publish key',self.run_cli('publish','repo','tag','probe.tcade',env=dict(self.env,TERMCADE_TOKEN=TOKEN),code=1).stderr)
        self.assertEqual(len(self.state.requests),count)

    def test_publish_key_create_list_and_revoke(self):
        self.sign_in();self.assertIn('no publish keys',self.run_cli('keys').stdout)
        created=self.run_cli('keys','new','release','player')
        self.assertIn(KEY,created.stdout);self.assertIn('shown once',created.stderr)
        listed=self.run_cli('keys','list').stdout;self.assertIn('release',listed);self.assertNotIn(KEY,listed)
        self.assertIn('revoked',self.run_cli('keys','revoke','key_fixture').stdout)

    def test_studio_lifecycle_roles_and_explicit_delete_confirmation(self):
        self.sign_in();self.run_cli('org','new','studio','our bio')
        self.assertIn('studio',self.run_cli('org','list').stdout)
        self.run_cli('org','add','studio','member@example.test','admin')
        self.assertIn('member@example.test',self.run_cli('org','show','studio').stdout)
        self.run_cli('org','edit','studio','new bio')
        self.assertIn('new bio',self.state.requests[-1][2]['bio'])
        before=len(self.state.requests);self.assertIn('to confirm',self.run_cli('org','delete','studio',code=1).stderr)
        self.assertEqual(len(self.state.requests),before)
        self.run_cli('org','remove','studio','member@example.test')
        self.run_cli('org','delete','studio','studio')

    def test_username_availability_and_saved_identity_follow_a_rename(self):
        self.assertIn('is available',self.run_cli('username','new-player').stdout)
        self.sign_in();self.assertIn('you are new-player',self.run_cli('username','new-player').stdout)
        self.assertEqual(json.loads(self.session.read_text())['username'],'new-player')

    def test_account_delete_confirmation_precedes_any_delete_and_clears_login(self):
        self.sign_in();self.assertIn('to confirm',self.run_cli('account','delete','wrong',code=1).stderr)
        self.assertFalse(any(method=='DELETE' for method,_,_,_ in self.state.requests))
        self.assertIn('account deleted',self.run_cli('account','delete','player').stdout)
        self.assertFalse(self.session.exists())

    def test_logout_is_idempotent(self):
        self.sign_in();self.run_cli('logout');self.assertFalse(self.session.exists());self.run_cli('logout')

    @unittest.skipIf(REFERENCE,'Rust authoring replaces the old Go source template')
    def test_generated_rust_game_builds_and_installs_without_go(self):
        game=self.root/'game with spaces'
        self.run_cli('dev','new','test/my-game',game)
        manifest=(game/'Cargo.toml').read_text()
        self.assertRegex(manifest,r'rev = "[0-9a-f]{40}"')
        self.assertIn('https://github.com/aviorstudio/termcade',manifest)
        self.assertFalse(list(game.rglob('*.go')))
        self.assertIn('already exists and is not empty',self.run_cli('dev','new','test/my-game',game,code=1).stderr)
        # Exercise the generated source against this exact SDK without fetching a
        # second Git checkout; the original immutable source pin is asserted above.
        local=f'termcade-sdk = {{ path = {json.dumps(str(ROOT/"sdk"))} }}'
        manifest=re.sub(r'termcade-sdk = \{[^\n]+\}',lambda _:local,manifest)
        (game/'Cargo.toml').write_text(manifest)
        self.run_cli('dev','build',game,timeout=300)
        package=game/'build/my-game.tcade';self.assertTrue(package.is_file())
        with zipfile.ZipFile(package) as archive:
            self.assertEqual(set(archive.namelist()),{'game.wasm','termcade.toml'})
            self.assertEqual(archive.read('game.wasm')[:4],b'\0asm')
        self.run_cli('dev','install',package)
        self.assertTrue((self.root/'data/termcade/games/test/my-game/game.wasm').is_file())
        good_package=package.read_bytes()
        (game/'src/lib.rs').write_text('compile_error!("fixture compile failure");\n',encoding='utf-8')
        failed=self.run_cli('dev','build',game,code=1,timeout=300)
        self.assertIn('fixture compile failure',failed.stderr)
        self.assertNotIn('"reason":"compiler-artifact"',failed.stderr)
        self.assertEqual(package.read_bytes(),good_package,'failed build replaced the working package')


def main():
    global CLI,WASM,REFERENCE,EXPECTED_VERSION
    parser=argparse.ArgumentParser()
    parser.add_argument('--binary',required=True,type=Path)
    parser.add_argument('--wasm',required=True,type=Path)
    parser.add_argument('--reference',action='store_true')
    parser.add_argument('--expected-version',default='dev')
    args,rest=parser.parse_known_args()
    CLI=args.binary.resolve();WASM=args.wasm.resolve();REFERENCE=args.reference;EXPECTED_VERSION=args.expected_version
    # Set the skip condition after argument parsing, before unittest discovery.
    if REFERENCE:
        Journeys.test_generated_rust_game_builds_and_installs_without_go.__unittest_skip__=True
        Journeys.test_generated_rust_game_builds_and_installs_without_go.__unittest_skip_why__='Rust authoring contract'
    unittest.main(argv=['test_rust_cli.py',*rest],verbosity=2)

if __name__=='__main__':
    main()
