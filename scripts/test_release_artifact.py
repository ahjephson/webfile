import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('release-artifact.py')
SPEC = importlib.util.spec_from_file_location('release_artifact', SCRIPT)
ARTIFACT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ARTIFACT)


class ReleaseArtifactTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.version = '1.2.3'
        self.commit = 'a' * 40
        self.archive = self.directory / 'webfile-1.2.3-linux-amd64.tar.gz'
        self.archive.write_bytes(b'tested package bytes')
        self.checksum = Path(str(self.archive) + '.sha256')
        self.write_checksum()

    def write_checksum(self):
        value = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.checksum.write_text(f'{value}  {self.archive.name}\n')

    def command(self, operation, commit=None):
        return subprocess.run([
            sys.executable, str(SCRIPT), operation, '--directory', str(self.directory),
            '--version', self.version, '--commit', commit or self.commit,
        ], capture_output=True, text=True)

    def test_tested_package_round_trip(self):
        self.assertEqual(self.command('create').returncode, 0)
        self.assertEqual(self.command('verify').returncode, 0)
        manifest = json.loads((self.directory / 'release-candidate.json').read_text())
        self.assertEqual(manifest['commit'], self.commit)
        self.assertEqual(manifest['version'], self.version)

    def test_different_commit_cannot_be_published(self):
        self.assertEqual(self.command('create').returncode, 0)
        result = self.command('verify', 'b' * 40)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('provenance', result.stderr)

    def test_modified_package_and_rewritten_checksum_cannot_be_published(self):
        self.assertEqual(self.command('create').returncode, 0)
        self.archive.write_bytes(b'replacement package bytes')
        self.write_checksum()
        result = self.command('verify')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('provenance', result.stderr)

    def test_wrong_recorded_version_cannot_be_published(self):
        self.assertEqual(self.command('create').returncode, 0)
        path = self.directory / 'release-candidate.json'
        record = json.loads(path.read_text())
        record['version'] = '9.9.9'
        path.write_text(json.dumps(record))
        self.assertNotEqual(self.command('verify').returncode, 0)

    def test_only_completed_successful_main_validation_matches(self):
        good = {'id': 10, 'head_sha': self.commit, 'head_branch': 'main',
                'event': 'push', 'status': 'completed', 'conclusion': 'success'}
        for changes in [{'head_sha': 'b' * 40}, {'head_branch': 'feature'},
                        {'event': 'pull_request'}, {'status': 'in_progress'},
                        {'conclusion': 'failure'}, {'conclusion': 'cancelled'}]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                ARTIFACT.select_run({'workflow_runs': [good | changes]}, self.commit)
        manual = good | {'id': 11, 'event': 'workflow_dispatch'}
        self.assertEqual(ARTIFACT.select_run({'workflow_runs': [good, manual]}, self.commit), 11)

    def test_bad_checksum_missing_record_and_invalid_identity_are_rejected(self):
        self.assertNotEqual(self.command('verify').returncode, 0)
        self.checksum.write_text('incorrect checksum\n')
        self.assertNotEqual(self.command('create').returncode, 0)
        for version, commit in [('1.2.3', 'short'), ('../escape', self.commit), ('v1.2.3', self.commit)]:
            with self.assertRaises(ValueError):
                ARTIFACT.manifest(self.directory, version, commit)


if __name__ == '__main__':
    unittest.main()
