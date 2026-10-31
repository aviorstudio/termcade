#!/usr/bin/env python3
"""Real PTY acceptance with an independent VT emulator and loopback API."""
import argparse
import codecs
import contextlib
import fcntl
import json
import hashlib
import os
from pathlib import Path
import pty
import selectors
import shlex
import signal
import struct
import subprocess
import tempfile
import termios
import time
import unittest
import pyte
import test_rust_cli as fixtures

CLI=None
WASM=None

class Arcade:
    def __init__(self, binary, env, cwd, width=80, height=24):
        self.master,self.slave=pty.openpty()
        self.before=termios.tcgetattr(self.slave)
        fcntl.ioctl(self.slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
        def attach():
            os.setsid()
            fcntl.ioctl(0,termios.TIOCSCTTY,0)
        self.process=subprocess.Popen([str(binary)],stdin=self.slave,stdout=self.slave,stderr=self.slave,
                                      cwd=cwd,env=dict(env,TERM='xterm-256color'),preexec_fn=attach)
        os.set_blocking(self.master,False)
        self.selector=selectors.DefaultSelector()
        self.selector.register(self.master,selectors.EVENT_READ)
        self.screen=pyte.Screen(width,height)
        self.screen.write_process_input=lambda text:os.write(self.master,text.encode())
        self.stream=pyte.Stream(self.screen)
        self.decoder=codecs.getincrementaldecoder('utf-8')('replace')
        self.raw=bytearray()
    def pump(self, duration=.05):
        for _,_ in self.selector.select(duration):
            with contextlib.suppress(BlockingIOError,OSError):
                data=os.read(self.master,65536)
                if data:
                    self.raw.extend(data)
                    self.stream.feed(self.decoder.decode(data))
    def text(self):
        return '\n'.join(self.screen.display)
    def wait(self, predicate, label, timeout=10):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            self.pump()
            if predicate():
                return
            if self.process.poll() is not None:
                break
        raise AssertionError(f'{label}\n{self.text()}\nexit={self.process.poll()}')
    def contains(self, text, timeout=10):
        self.wait(lambda:text in self.text(),f'missing {text!r}',timeout)
    def settled(self):
        self.wait(lambda:'loading' not in self.text(),'request settled')
    def send(self, data):
        os.write(self.master,data)
    def resize(self,width,height):
        self.screen.resize(lines=height,columns=width)
        fcntl.ioctl(self.slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
        self.process.send_signal(signal.SIGWINCH)
    def finish(self, termination=None):
        if termination:
            self.process.send_signal(termination)
        else:
            self.send(b'\x03')
        deadline=time.monotonic()+10
        while self.process.poll() is None and time.monotonic()<deadline:
            self.pump()
        if self.process.poll() is None:
            raise AssertionError('arcade did not stop')
        self.pump()
        if self.process.returncode != 0:
            raise AssertionError(f'arcade exited {self.process.returncode}: {self.text()}')
        if termios.tcgetattr(self.slave) != self.before:
            raise AssertionError('terminal mode was not restored')
        for sequence in [b'\x1b[?1049l',b'\x1b[?2004l',b'\x1b[?1004l',b'\x1b[<1u',b'\x1b[?25h']:
            if sequence not in self.raw:
                raise AssertionError(f'missing terminal restoration sequence {sequence!r}')
    def close(self):
        if self.process.poll() is None:
            os.killpg(self.process.pid,signal.SIGKILL)
            self.process.wait(timeout=5)
        self.selector.close()
        os.close(self.master)
        os.close(self.slave)

class TuiJourneys(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='termcade-tui-contract-')
        self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name)
        self.state=fixtures.State(WASM.read_bytes())
        self.server=fixtures.marketplace(self.state)
        self.base=self.server.__enter__()
        self.addCleanup(self.server.__exit__,None,None,None)
        original=self.state.handle
        def handle(method,path,body,auth):
            if path == '/v1/games/test/probe':
                self.state.requests.append((method,path,body,auth))
                return 200,self.state.game
            if path.endswith('/plays'):
                self.state.requests.append((method,path,body,auth))
                return 200,dict(days=[dict(day='2026-01-01',count=0)])
            return original(method,path,body,auth)
        self.state.handle=handle
        self.env=dict(os.environ,XDG_CONFIG_HOME=str(self.root/'config'),XDG_DATA_HOME=str(self.root/'data'),
                      TERMCADE_REGISTRY=self.base,TERMCADE_DEV_APP_URL=self.base,TERMCADE_PIXELS='quad')
        self.env.pop('TERMCADE_TOKEN',None)
        self.session=self.root/'config/termcade/local'/hashlib.sha256(self.base.encode()).hexdigest()[:24]/'session.json'
        self.installed=self.root/'data/termcade/games/test/probe'
    def sign_in(self):
        self.session.parent.mkdir(parents=True,exist_ok=True)
        self.session.write_text(json.dumps(dict(registry=self.base,email='player@example.test',username='player',token=fixtures.TOKEN,credential_id='cli_fixture')))
        self.session.chmod(0o600)
    def arcade(self):
        app=Arcade(CLI,self.env,self.root)
        self.addCleanup(app.close)
        return app
    def test_account_add_play_pause_local_uninstall_and_account_remove_are_distinct(self):
        self.sign_in()
        app=self.arcade()
        app.contains('test/probe')
        app.wait(lambda:any(p=='/v1/library'for _,p,_,_ in self.state.requests),'account snapshot loaded')
        app.send(b'\x1b[B\r')
        app.contains('Add to your Library to play')
        app.settled()
        app.send(b'a')
        app.contains('In your Library')
        app.settled()
        self.assertEqual(len(self.state.library),1)
        self.assertFalse(self.installed.exists())
        self.assertFalse(any(p.endswith('/download')for _,p,_,_ in self.state.requests))
        app.send(b'p')
        app.contains('Esc / P pause')
        app.send(b'\x1b')
        app.contains('Paused')
        self.assertTrue((self.installed/'game.wasm').is_file())
        app.send(b'\t\r')
        app.contains('Marketplace')
        app.send(b'\x1b[B\r')
        app.contains('Installed 0.1.0')
        app.settled()
        app.send(b'u')
        app.contains('Type test/probe to confirm')
        app.send(b'test/probe\t\r')
        app.contains('In your Library')
        app.settled()
        self.assertFalse(self.installed.exists())
        self.assertEqual(len(self.state.library),1)
        # Tab enters the action row; its fourth action removes account membership.
        app.send(b'\t\x1b[B\x1b[B\x1b[B')
        app.contains('‹ Remove from Library ›')
        app.send(b'\r')
        app.contains('Add to your Library to play')
        app.settled()
        self.assertEqual(self.state.library,[])
        app.finish()
        scores=json.loads((self.root/'config/termcade/scores.json').read_text())
        self.assertEqual(scores['games']['test/probe']['version'],'0.1.0')
        self.assertTrue(scores['games']['test/probe']['last_played'])
    def test_pairing_is_explicit_small_dialogs_are_inert_and_sigterm_restores_terminal(self):
        self.state.pending=True
        bindir=self.root/'bin'
        bindir.mkdir()
        opened=self.root/'opened-browser'
        launcher=bindir/'xdg-open'
        launcher.write_text('#!/bin/sh\nprintf "%s\\n" "$@" >> '+shlex.quote(str(opened))+'\n')
        launcher.chmod(0o755)
        self.env['PATH']=str(bindir)+os.pathsep+self.env['PATH']
        app=self.arcade()
        app.contains('Marketplace')
        app.send(b's')
        app.contains('Approve this terminal in your browser')
        app.send(b'\r')
        app.contains('ABCD-EFGH')
        self.assertFalse(opened.exists(),'browser opened without an explicit action')
        app.send(b'\r')
        app.wait(opened.exists,'explicit browser action')
        self.assertEqual(opened.read_text().strip(),self.base+'/pair/ABCD-EFGH')
        app.resize(30,10)
        app.contains('Resize to at least 40')
        app.send(b'\r')
        app.pump(.1)
        self.assertEqual(len(opened.read_text().splitlines()),1,'hidden button opened a browser')
        app.resize(80,24)
        app.contains('ABCD-EFGH')
        app.send(b'\x1b')
        app.contains('Settings')
        self.assertFalse(self.session.exists())
        app.finish(signal.SIGTERM)

def main():
    global CLI,WASM
    parser=argparse.ArgumentParser()
    parser.add_argument('--binary',required=True,type=Path)
    parser.add_argument('--wasm',required=True,type=Path)
    args,rest=parser.parse_known_args()
    CLI=args.binary.resolve();WASM=args.wasm.resolve()
    unittest.main(argv=['test_rust_tui.py',*rest],verbosity=2)
if __name__=='__main__':
    main()
