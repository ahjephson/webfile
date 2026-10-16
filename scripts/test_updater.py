"""Exercise the shipped updater inside a disposable, unprivileged installation."""
import hashlib
import io
import os
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
                data = content.encode()
                info.size = len(data)
                info.mode = 0o755 if name in ('webfile', 'deploy/webfile-update') else 0o644
                output.addfile(info, io.BytesIO(data))
        checksum = root / 'release.tar.gz.sha256'
        checksum.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + '  release.tar.gz\n')

        def executable(path, content):
            path.write_text(content)
            path.chmod(0o755)

        executable(commands / 'curl', '''#!/usr/bin/python3
import os, shutil, sys
source = os.environ['TEST_CHECKSUM'] if any(a.endswith('.sha256') for a in sys.argv) else os.environ['TEST_ARCHIVE']
shutil.copyfile(source, sys.argv[sys.argv.index('-o') + 1])
''')
        executable(commands / 'systemctl', '''#!/usr/bin/python3
import os, pathlib, sys
marker = pathlib.Path(os.environ['TEST_FAILED'])
command = sys.argv[1]
fail = not marker.exists() and ((os.environ['TEST_FAULT'] == 'restart' and command == 'restart') or
                              (os.environ['TEST_FAULT'] == 'health' and command == 'is-active'))
if fail: marker.touch()
sys.exit(1 if fail else 0)
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
               'TEST_FAULT': fault, 'TEST_FAILED': str(root / 'failed')}
        result = subprocess.run(['bash', str(script), 'https://test.invalid/release.tar.gz',
                                 'https://test.invalid/release.tar.gz.sha256'],
                                env=env, text=True, capture_output=True, timeout=15)
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


if __name__ == '__main__':
    unittest.main()
