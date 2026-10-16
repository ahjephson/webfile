"""Exercise the shipped updater inside a disposable, unprivileged installation."""
import hashlib
import io
import os
import signal
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

REPOSITORY = Path(__file__).resolve().parents[1]
TARGETS = {
    '/usr/local/bin/webfile': 'webfile',
    '/usr/local/sbin/webfile-update': 'deploy/webfile-update',
    '/etc/systemd/system/webfile.service': 'deploy/webfile.service',
    '/etc/systemd/system/webfile-launcher.service': 'deploy/webfile-launcher.service',
    '/usr/lib/tmpfiles.d/webfile.conf': 'deploy/webfile.tmpfiles',
    '/etc/pam.d/webfile': 'deploy/webfile.pam',
}


class UpdaterTests(unittest.TestCase):
    def run_update(self, fault='none', updater_exists=True):
        temporary = tempfile.TemporaryDirectory(prefix='webfile-updater-test-')
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name).resolve()
        commands = root / 'commands'
        commands.mkdir()
        work = root / 'work'
        work.mkdir()
        installed = {}
        for target in TARGETS:
            path = root / target.lstrip('/')
            self.assertTrue(path.resolve().is_relative_to(root))
            path.parent.mkdir(parents=True, exist_ok=True)
            if updater_exists or target != '/usr/local/sbin/webfile-update':
                path.write_text('old installation\n')
                path.chmod(0o755 if '/usr/local/' in target else 0o644)
            installed[target] = path
        contents = {name: 'new ' + name + '\n' for name in TARGETS.values()}
        contents['webfile'] = '#!/bin/sh\necho "webfile test-new"\n'
        archive = root / 'release.tar.gz'
        with tarfile.open(archive, 'w:gz') as output:
            for name, content in contents.items():
                info = tarfile.TarInfo('package/' + name)
                data = Path('/usr/bin/sleep').read_bytes() if fault == 'running-binary' and name == 'webfile' else content.encode()
                info.size = len(data)
                info.mode = 0o755 if name in ('webfile', 'deploy/webfile-update') else 0o644
                output.addfile(info, io.BytesIO(data))
        checksum = root / 'release.tar.gz.sha256'
        digest = '0' * 64 if fault == 'checksum' else hashlib.sha256(archive.read_bytes()).hexdigest()
        checksum.write_text(digest + '  release.tar.gz\n')

        def executable(path, content):
            path.write_text(content)
            path.chmod(0o755)

        executable(commands / 'curl', '''#!/usr/bin/python3
import os, shutil, sys
source = os.environ['TEST_CHECKSUM'] if any(a.endswith('.sha256') for a in sys.argv) else os.environ['TEST_ARCHIVE']
shutil.copyfile(source, sys.argv[sys.argv.index('-o') + 1])
''')
        executable(commands / 'systemctl', '''#!/usr/bin/python3
import os, pathlib, subprocess, sys
marker = pathlib.Path(os.environ['TEST_FAILED'])
command = sys.argv[1]
if os.environ['TEST_FAULT'] == 'running-binary' and command == 'restart' and not marker.exists():
    process = subprocess.Popen([os.environ['TEST_BINARY'], '30'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    pathlib.Path(os.environ['TEST_CHILD']).write_text(str(process.pid))
    marker.touch()
    sys.exit(1)
fail = not marker.exists() and ((os.environ['TEST_FAULT'] in ('daemon', 'rollback') and command == 'daemon-reload') or
                              (os.environ['TEST_FAULT'] == 'restart' and command == 'restart') or
                              (os.environ['TEST_FAULT'] == 'health' and command == 'is-active'))
if fail: marker.touch()
sys.exit(1 if fail else 0)
''')
        executable(commands / 'install', '''#!/usr/bin/python3
import os, sys
fault = os.environ['TEST_FAULT']
source, target = sys.argv[-2:]
if ((fault == 'install' and target.endswith('webfile-launcher.service')) or
    (fault == 'binary' and source.endswith('/release/webfile')) or
    (fault == 'updater' and source.endswith('/release/deploy/webfile-update'))):
    sys.exit(1)
os.execv('/usr/bin/install', ['install', *sys.argv[1:]])
''')
        executable(commands / 'cp', '''#!/usr/bin/python3
import os, sys
if os.environ['TEST_FAULT'] == 'backup' and sys.argv[-1].endswith('/previous/webfile.service'):
    sys.exit(1)
if os.environ['TEST_FAULT'] == 'rollback' and sys.argv[-2].endswith('/previous/webfile.service'):
    sys.exit(1)
os.execv('/usr/bin/cp', ['cp', *sys.argv[1:]])
''')
        executable(commands / 'sleep', '#!/bin/sh\nexit 0\n')
        source = (REPOSITORY / 'deploy/webfile-update').read_text()
        guard = '[[ $EUID -eq 0 ]] || { echo "Run as root (sudo)." >&2; exit 1; }'
        self.assertEqual(source.count(guard), 1)
        source = source.replace(guard, ': # Only the isolated test copy runs unprivileged.')
        for prefix in ('/usr/local/bin', '/usr/local/sbin', '/etc/systemd/system', '/usr/lib/tmpfiles.d', '/etc/pam.d'):
            source = source.replace(prefix, str(root / prefix.lstrip('/')))
        script = root / 'updater-under-test'
        script.write_text(source)
        env = {**os.environ, 'PATH': f'{commands}:/usr/bin:/bin', 'TMPDIR': str(work),
               'TEST_ARCHIVE': str(archive), 'TEST_CHECKSUM': str(checksum),
               'TEST_FAULT': fault, 'TEST_FAILED': str(root / 'failed'),
               'TEST_BINARY': str(installed['/usr/local/bin/webfile']), 'TEST_CHILD': str(root / 'child')}
        try:
            result = subprocess.run(['bash', str(script), 'https://test.invalid/release.tar.gz',
                                     'https://test.invalid/release.tar.gz.sha256'],
                                    env=env, text=True, capture_output=True, timeout=15)
        finally:
            if (root / 'child').exists():
                try:
                    os.kill(int((root / 'child').read_text()), signal.SIGTERM)
                except ProcessLookupError:
                    pass
        return result, installed, contents

    def test_success_installs_release_updater_with_executable_permissions(self):
        result, installed, contents = self.run_update()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for target, source in TARGETS.items():
            self.assertEqual(installed[target].read_text(), contents[source], target)
        self.assertEqual(installed['/usr/local/sbin/webfile-update'].stat().st_mode & 0o777, 0o755)

    def test_startup_failures_restore_previous_updater_and_installation(self):
        for fault in ('restart', 'health'):
            with self.subTest(fault=fault):
                result, installed, _ = self.run_update(fault)
                self.assertNotEqual(result.returncode, 0)
                for target, path in installed.items():
                    self.assertEqual(path.read_text(), 'old installation\n', target)

    def test_failed_update_preserves_absence_of_previously_missing_updater(self):
        result, installed, _ = self.run_update('restart', updater_exists=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(installed['/usr/local/sbin/webfile-update'].exists())

    def test_every_installation_failure_restores_previous_installation(self):
        for fault in ('binary', 'updater', 'install', 'daemon', 'running-binary'):
            with self.subTest(fault=fault):
                result, installed, _ = self.run_update(fault)
                self.assertNotEqual(result.returncode, 0)
                for target, path in installed.items():
                    self.assertEqual(path.read_text(), 'old installation\n', target)
                for target in ('/usr/local/bin/webfile', '/usr/local/sbin/webfile-update'):
                    self.assertEqual(list(installed[target].parent.glob('.webfile-*')), [])
                    self.assertEqual(list(installed[target].parent.glob('*.rollback.*')), [])
                    self.assertFalse(Path(str(installed[target]) + '.new').exists())

    def test_preinstallation_failures_leave_files_untouched(self):
        for fault in ('checksum', 'backup'):
            with self.subTest(fault=fault):
                result, installed, _ = self.run_update(fault)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn('Restoring previous', result.stderr)
                for target, path in installed.items():
                    self.assertEqual(path.read_text(), 'old installation\n', target)

    def test_rollback_failure_is_reported_and_other_files_are_restored(self):
        result, installed, contents = self.run_update('rollback')
        self.assertEqual(result.returncode, 1)
        self.assertIn('Rollback was incomplete', result.stderr)
        for target, path in installed.items():
            expected = contents[TARGETS[target]] if target == '/etc/systemd/system/webfile.service' else 'old installation\n'
            self.assertEqual(path.read_text(), expected, target)
            self.assertEqual(list(path.parent.glob('*.rollback.*')), [])


if __name__ == '__main__':
    unittest.main()
