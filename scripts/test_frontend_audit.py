from copy import deepcopy
from datetime import date
import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location('frontend_audit', Path(__file__).with_name('audit-frontend.py'))
AUDIT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(AUDIT)
TODAY = date(2026, 11, 18)


class FrontendAuditTests(unittest.TestCase):
    def setUp(self):
        self.lock = {'packages': {f'node_modules/{name}': {'version': version, 'dev': True}
                                  for name, version in AUDIT.BUILD_PACKAGES.items()}}
        self.report = {'auditReportVersion': 2, 'vulnerabilities': {},
                       'metadata': {'vulnerabilities': {'high': 6, 'critical': 0}}}
        for name in AUDIT.BUILD_PACKAGES:
            self.report['vulnerabilities'][name] = {
                'severity': 'high', 'nodes': [f'node_modules/{name}'], 'via': ['picomatch'],
            }
        self.cause = {'name': 'picomatch', 'dependency': 'picomatch', 'url': AUDIT.ADVISORY, 'severity': 'high'}
        self.report['vulnerabilities']['picomatch']['via'] = [self.cause]
        self.report['vulnerabilities']['vue-tsc']['via'] = ['@vue/language-core']
        self.report['vulnerabilities']['tinyglobby']['via'] = ['fdir', 'picomatch']
        self.report['vulnerabilities']['vite']['via'] = ['fdir', 'tinyglobby', 'picomatch']

    def test_current_advisory_and_all_transitive_reports_are_excepted(self):
        accepted, blocked = AUDIT.evaluate(self.report, self.lock, TODAY)
        self.assertEqual(accepted, sorted(AUDIT.BUILD_PACKAGES))
        self.assertEqual(blocked, [])

    def test_clean_audit_needs_no_exception_even_after_expiry(self):
        self.report['vulnerabilities'] = {}
        self.report['metadata']['vulnerabilities']['high'] = 0
        self.assertEqual(AUDIT.evaluate(self.report, self.lock, AUDIT.EXPIRES), ([], []))

    def test_new_advisory_in_the_same_package_blocks_every_affected_parent(self):
        other = deepcopy(self.cause)
        other['url'] = 'https://github.com/advisories/GHSA-other-advisory'
        self.report['vulnerabilities']['picomatch']['via'].append(other)
        self.assertEqual(AUDIT.evaluate(self.report, self.lock, TODAY), ([], sorted(AUDIT.BUILD_PACKAGES)))

    def test_exception_expires_on_its_deadline(self):
        self.assertEqual(AUDIT.evaluate(self.report, self.lock, AUDIT.EXPIRES), ([], sorted(AUDIT.BUILD_PACKAGES)))

    def test_runtime_dependency_and_version_drift_block_the_exception(self):
        for changed in ({'dev': False}, {'version': '4.0.8'}):
            with self.subTest(changed=changed):
                lock = deepcopy(self.lock)
                lock['packages']['node_modules/picomatch'].update(changed)
                self.assertEqual(AUDIT.evaluate(self.report, lock, TODAY), ([], sorted(AUDIT.BUILD_PACKAGES)))

    def test_critical_reclassification_is_not_excepted(self):
        self.report['vulnerabilities']['picomatch']['severity'] = 'critical'
        self.cause['severity'] = 'critical'
        self.report['metadata']['vulnerabilities'].update(high=5, critical=1)
        self.assertEqual(AUDIT.evaluate(self.report, self.lock, TODAY), ([], sorted(AUDIT.BUILD_PACKAGES)))

    def test_unrelated_high_finding_still_blocks(self):
        self.report['vulnerabilities']['other-package'] = {'severity': 'high', 'nodes': ['node_modules/other-package'], 'via': [self.cause]}
        self.report['metadata']['vulnerabilities']['high'] += 1
        self.assertEqual(AUDIT.evaluate(self.report, self.lock, TODAY)[1], ['other-package'])

    def test_missing_cause_and_cycles_are_not_excepted(self):
        for cause in ('missing-package', 'picomatch'):
            with self.subTest(cause=cause):
                report = deepcopy(self.report)
                report['vulnerabilities']['picomatch']['via'] = [cause]
                self.assertEqual(AUDIT.evaluate(report, self.lock, TODAY)[0], [])

    def test_incomplete_and_failed_reports_fail_closed(self):
        for field, value in (('error', {'code': 'NETWORK_ERROR'}), ('auditReportVersion', 1),
                             ('vulnerabilities', None), ('metadata', {})):
            with self.subTest(field=field):
                report = deepcopy(self.report)
                report[field] = value
                with self.assertRaises(ValueError):
                    AUDIT.evaluate(report, self.lock, TODAY)


if __name__ == '__main__':
    unittest.main()
