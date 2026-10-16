"""Check browser-runner sharding and diagnostics without building or serving WebFile."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

RUNNER = Path(__file__).resolve().with_name('validate-browser.sh')
REPOSITORY = RUNNER.parent.parent


class BrowserValidationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='webfile-browser-runner-test-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'web' / 'test-results').mkdir(parents=True)
        (self.root / 'backend').mkdir()
        shutil.copyfile(RUNNER, self.root / 'scripts' / 'validate-browser.sh')
        shutil.copyfile(REPOSITORY / 'webfile.dev.json', self.root / 'webfile.dev.json')
        self.unrelated = self.root / 'web' / 'test-results' / 'previous-run.txt'
        self.unrelated.write_text('preserve previous diagnostics')
        commands = self.root / 'commands'
        commands.mkdir()

        def executable(name, source):
            path = commands / name
            path.write_text('#!/usr/bin/python3\n' + source)
            path.chmod(0o755)

        executable('npm', '')
        executable('curl', '')
        executable('go', '''import pathlib, sys
output = pathlib.Path(sys.argv[sys.argv.index('-o') + 1])
output.write_text('#!/bin/sh\\nexec /bin/sleep 60\\n')
output.chmod(0o755)
''')
        executable('npx', '''import json, os, pathlib, sys
record = {'args': sys.argv[1:], 'report': os.environ['PLAYWRIGHT_JSON_OUTPUT_FILE'],
          'prefix': os.environ.get('WEBFILE_E2E_PREFIX', '')}
with open(os.environ['TEST_BROWSER_CALLS'], 'a') as log:
    log.write(json.dumps(record) + '\\n')
output = next((a.split('=', 1)[1] for a in sys.argv if a.startswith('--output=')), 'test-results')
pathlib.Path(output).mkdir(parents=True, exist_ok=True)
pathlib.Path(output, 'marker.txt').write_text(record['prefix'] or 'main')
pathlib.Path(record['report']).write_text('{}')
phase = 'prefix' if record['prefix'] else 'main'
sys.exit(1 if os.environ.get('TEST_BROWSER_FAIL') == phase else 0)
''')
        self.environment = {**os.environ, 'PATH': str(commands) + os.pathsep + os.environ['PATH'],
                            'TEST_BROWSER_CALLS': str(self.root / 'calls.jsonl')}
        for name in ('WEBFILE_E2E_SHARD', 'WEBFILE_E2E_PREFIX', 'WEBFILE_E2E_PORT'):
            self.environment.pop(name, None)

    def run_validation(self, shard='', selectors=(), fail=''):
        return subprocess.run(['bash', str(self.root / 'scripts' / 'validate-browser.sh'),
                               'webkit-ipad', *selectors], cwd=self.root,
                              env={**self.environment, 'WEBFILE_E2E_SHARD': shard,
                                   'TEST_BROWSER_FAIL': fail}, capture_output=True, text=True, timeout=15)

    def calls(self):
        return [json.loads(line) for line in (self.root / 'calls.jsonl').read_text().splitlines()]

    def test_complete_default_suite_and_prefix_keep_separate_diagnostics(self):
        result = self.run_validation()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Full browser validation: webkit-ipad', result.stdout)
        main, prefix = self.calls()
        self.assertFalse(any(a.startswith('--shard') for a in main['args'] + prefix['args']))
        self.assertEqual(prefix['prefix'], '/tools/webfile')
        work = self.root / 'dist' / 'validation' / 'webkit-ipad'
        self.assertIn('--output=' + str(work / 'test-results'), main['args'])
        self.assertIn('--output=' + str(work / 'test-results-prefix'), prefix['args'])
        self.assertEqual((work / 'test-results' / 'marker.txt').read_text(), 'main')
        self.assertEqual((work / 'test-results-prefix' / 'marker.txt').read_text(), '/tools/webfile')
        self.assertEqual(self.unrelated.read_text(), 'preserve previous diagnostics')

    def test_shards_preserve_coverage_arguments_and_do_not_shard_prefix_check(self):
        result = self.run_validation('2/4', ('tests/e2e/media-updates.spec.ts', '--grep', 'open gallery'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Focused browser validation:', result.stdout)
        self.assertIn('Browser shard: 2/4', result.stdout)
        main, prefix = self.calls()
        self.assertIn('--shard=2/4', main['args'])
        self.assertIn('tests/e2e/media-updates.spec.ts', main['args'])
        self.assertIn('open gallery', main['args'])
        self.assertFalse(any(a.startswith('--shard') for a in prefix['args']))
        self.assertIn('/shard-2-of-4/', main['report'])
        self.assertIn('/shard-2-of-4/', prefix['report'])

    def test_different_shards_keep_independent_configuration_and_reports(self):
        for shard in ('2/4', '3/4'):
            result = self.run_validation(shard)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('Full browser validation:', result.stdout)
        configurations = []
        for index in (2, 3):
            work = self.root / 'dist' / 'validation' / 'webkit-ipad' / f'shard-{index}-of-4'
            self.assertTrue((work / 'results.json').is_file())
            self.assertTrue((work / 'results-prefix.json').is_file())
            configurations.append(json.loads((work / 'config.json').read_text()))
        self.assertNotEqual(configurations[0]['roots'], configurations[1]['roots'])
        self.assertNotEqual(configurations[0]['runtime'], configurations[1]['runtime'])

    def test_invalid_shard_fails_before_build_or_test(self):
        for shard in ('0/4', '5/4', '1/0', '2/04', '../4', '1/4 --grep test', '2', '-1/4'):
            with self.subTest(shard=shard):
                result = self.run_validation(shard)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('WEBFILE_E2E_SHARD must be INDEX/TOTAL', result.stderr)
        self.assertFalse((self.root / 'calls.jsonl').exists())
        self.assertFalse((self.root / 'dist').exists())

    def test_failed_suite_retains_its_diagnostics_and_does_not_run_prefix(self):
        result = self.run_validation('1/4', fail='main')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)
        work = self.root / 'dist' / 'validation' / 'webkit-ipad' / 'shard-1-of-4'
        self.assertEqual((work / 'test-results' / 'marker.txt').read_text(), 'main')
        self.assertTrue(self.unrelated.exists())

    def test_prefix_failure_is_not_hidden_by_successful_shard(self):
        result = self.run_validation('4/4', fail='prefix')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 2)


if __name__ == '__main__':
    unittest.main()
