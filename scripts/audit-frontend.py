#!/usr/bin/env python3
"""Audit every frontend dependency, with one bounded build-tool exception."""

from datetime import date, datetime, timezone
import json
from pathlib import Path
import subprocess
import sys


ADVISORY = 'https://github.com/advisories/GHSA-fc5q-39xc-vjpf'
EXPIRES = date(2026, 12, 18)
# Scope and exposure review: docs/SECURITY.md, "Temporary dependency exception".
BUILD_PACKAGES = {
    'picomatch': '4.0.7',
    '@vue/language-core': '3.3.14',
    'vue-tsc': '3.3.14',
    'fdir': '6.5.0',
    'tinyglobby': '0.2.17',
    'vite': '6.4.6',
}
SEVERITIES = {'info', 'low', 'moderate', 'high', 'critical'}


def evaluate(report, lock, today):
    if report.get('error') or report.get('auditReportVersion') != 2:
        raise ValueError('npm did not return a complete version-2 audit report')
    findings = report.get('vulnerabilities')
    packages = lock.get('packages')
    if not isinstance(findings, dict) or not isinstance(packages, dict):
        raise ValueError('missing audit findings or locked packages')
    counts = report.get('metadata', {}).get('vulnerabilities', {})
    for severity in ('high', 'critical'):
        actual = sum(item.get('severity') == severity for item in findings.values())
        if counts.get(severity) != actual:
            raise ValueError('audit severity counts do not match the findings')
    for finding in findings.values():
        if finding.get('severity') not in SEVERITIES or not isinstance(finding.get('via'), list) or not finding['via']:
            raise ValueError('incomplete audit finding')

    def excepted(name, visiting):
        if today >= EXPIRES or name not in BUILD_PACKAGES or name in visiting:
            return False
        finding = findings.get(name)
        if not finding or finding['severity'] != 'high':
            return False
        node = f'node_modules/{name}'
        locked = packages.get(node, {})
        if finding.get('nodes') != [node] or locked.get('version') != BUILD_PACKAGES[name] or locked.get('dev') is not True:
            return False
        for cause in finding['via']:
            if isinstance(cause, str):
                if not excepted(cause, visiting | {name}):
                    return False
            elif isinstance(cause, dict):
                if not (name == 'picomatch' and cause.get('name') == 'picomatch'
                        and cause.get('dependency') == 'picomatch' and cause.get('url') == ADVISORY
                        and cause.get('severity') == 'high'):
                    return False
            else:
                return False
        return True

    accepted, blocked = [], []
    for name, finding in findings.items():
        if finding['severity'] in ('high', 'critical'):
            (accepted if excepted(name, set()) else blocked).append(name)
    return sorted(accepted), sorted(blocked)


def main():
    root = Path(__file__).resolve().parents[1]
    result = subprocess.run(['npm', 'audit', '--json', '--audit-level=high'], cwd=root / 'web', capture_output=True, text=True)
    try:
        if result.returncode not in (0, 1):
            raise ValueError(f'npm audit exited with status {result.returncode}')
        report = json.loads(result.stdout)
        lock = json.loads((root / 'web/package-lock.json').read_text())
        accepted, blocked = evaluate(report, lock, datetime.now(timezone.utc).date())
    except (ValueError, KeyError, TypeError, AttributeError, OSError) as error:
        print(f'Frontend audit failed: {error}', file=sys.stderr)
        return 1
    if accepted:
        print(f'Temporary build-tool exception: {ADVISORY}')
        print(f'Exact locked development packages: {", ".join(accepted)}; expires {EXPIRES} UTC.')
        print('Exposure and removal conditions: docs/SECURITY.md')
    if blocked:
        print(f'Blocking high/critical findings: {", ".join(blocked)}', file=sys.stderr)
        print(json.dumps(report, indent=2), file=sys.stderr)
        return 1
    print('Frontend audit passed: no unexcepted high/critical findings.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
