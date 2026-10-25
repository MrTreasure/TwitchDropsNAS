"""Private state and single-source credential persistence."""
import datetime as dt
import fcntl
import hashlib
import json
import os
import sqlite3
import tempfile
from pathlib import Path

import yaml


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


class Store:
    def __init__(self, directory):
        path = Path(directory)
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.db = sqlite3.connect(path / 'state.sqlite')
        self.db.executescript('''PRAGMA journal_mode=WAL;
        CREATE TABLE IF NOT EXISTS state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, at TEXT, level TEXT, message TEXT);
        CREATE TABLE IF NOT EXISTS traffic (at REAL, route TEXT, bytes INTEGER);
        CREATE INDEX IF NOT EXISTS traffic_at ON traffic(at);
        ''')

    def get(self, key, default=None):
        row = self.db.execute('SELECT value FROM state WHERE key=?', (key,)).fetchone()
        return json.loads(row[0]) if row else default

    def put(self, key, value):
        self.db.execute('INSERT OR REPLACE INTO state VALUES (?,?)', (key, json.dumps(value, ensure_ascii=False)))
        self.db.commit()

    def event(self, message, level='info'):
        # Callers pass fixed product messages only, never upstream bodies/errors.
        self.db.execute('INSERT INTO events(at,level,message) VALUES (?,?,?)', (now(), level, message))
        self.db.execute('DELETE FROM events WHERE id < (SELECT MAX(id)-500 FROM events)')
        self.db.commit()

    def events(self):
        return [dict(zip(['id', 'at', 'level', 'message'], row)) for row in self.db.execute(
            'SELECT id,at,level,message FROM events ORDER BY id DESC LIMIT 80')]

    def traffic(self, timestamp, route, count):
        self.db.execute('INSERT INTO traffic VALUES (?,?,?)', (timestamp, route, count))
        self.db.execute('DELETE FROM traffic WHERE at < ?', (timestamp - 172800,))
        self.db.commit()

    def traffic_since(self, timestamp, route=None):
        query = 'SELECT COALESCE(SUM(bytes),0) FROM traffic WHERE at>=?'
        args = [timestamp]
        if route:
            query += ' AND route=?'
            args.append(route)
        return self.db.execute(query, args).fetchone()[0]


class SecretBook:
    """Only twitch_drops is modified. No token or book copy is written elsewhere."""
    def __init__(self, path):
        self.path = Path(path)

    def read(self):
        book = yaml.safe_load(self.path.read_text()) or {}
        return book.get('twitch_drops', {}).get('account')

    def write(self, account):
        lock = self.path.with_name('.twitch-drops-book.lock')
        with lock.open('a') as handle:
            os.chmod(lock, 0o600)
            fcntl.flock(handle, fcntl.LOCK_EX)
            original = self.path.read_bytes()
            book = yaml.safe_load(original) or {}
            namespace = book.setdefault('twitch_drops', {})
            if account is None:
                namespace.pop('account', None)
            else:
                namespace['account'] = account
            # Preserve the rest of the book's bytes, comments and formatting.
            # Compose provides exact top-level source spans; never regex-match secrets.
            composed = yaml.compose(original.decode())
            span = None
            if composed:
                entries = composed.value
                for index, (key, _value) in enumerate(entries):
                    if key.value == 'twitch_drops':
                        end = entries[index + 1][0].start_mark.index if index + 1 < len(entries) else len(original.decode())
                        span = (key.start_mark.index, end)
                        break
            block = yaml.safe_dump({'twitch_drops': namespace}, allow_unicode=True, sort_keys=False)
            text = original.decode()
            updated = text[:span[0]] + block + text[span[1]:] if span else text.rstrip() + '\n\n' + block
            if yaml.safe_load(updated) != book:
                raise RuntimeError('凭据保存校验失败，旧记录已保留')
            fd, temporary = tempfile.mkstemp(prefix='.twitch-drops-', dir=self.path.parent)
            try:
                os.fchmod(fd, 0o600)
                with os.fdopen(fd, 'w') as target:
                    target.write(updated)
                    target.flush()
                    os.fsync(target.fileno())
                if hashlib.sha256(self.path.read_bytes()).digest() != hashlib.sha256(original).digest():
                    raise RuntimeError('密码本同时发生修改，请重新连接')
                os.replace(temporary, self.path)
            finally:
                if os.path.exists(temporary):
                    os.unlink(temporary)


class FileCredentials:
    """Single private token file for standalone installations; never returned by API."""
    def __init__(self, path):
        self.path = Path(path)

    def read(self):
        if not self.path.exists():
            return None
        return json.loads(self.path.read_text()).get('account')

    def write(self, account):
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        fd, temporary = tempfile.mkstemp(prefix='.credentials-', dir=self.path.parent)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, 'w') as handle:
                json.dump({'account': account}, handle)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, self.path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
