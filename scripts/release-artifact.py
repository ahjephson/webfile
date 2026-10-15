#!/usr/bin/env python3
"""Bind a tested release package to its source commit and version."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def manifest(directory, version, commit):
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', version):
        raise ValueError('version must be a bare semantic version')
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('commit must be a full Git SHA')
    archive = f'webfile-{version}-linux-amd64.tar.gz'
    checksum = archive + '.sha256'
    archive_digest = digest(directory / archive)
    expected = f'{archive_digest}  {archive}\n'
    if (directory / checksum).read_text() != expected:
        raise ValueError('package checksum does not match the archive')
    return {
        'version': version,
        'commit': commit,
        'files': {archive: archive_digest, checksum: digest(directory / checksum)},
    }


def select_run(payload, commit):
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('commit must be a full Git SHA')
    runs = [run for run in payload.get('workflow_runs', []) if
            run.get('head_sha') == commit and run.get('head_branch') == 'main' and
            run.get('event') in ('push', 'workflow_dispatch') and
            run.get('status') == 'completed' and run.get('conclusion') == 'success']
    if not runs:
        raise ValueError('no successful main validation for this exact commit; validate before tagging')
    return max(run['id'] for run in runs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['create', 'verify', 'select-run'])
    parser.add_argument('--directory', type=Path)
    parser.add_argument('--version')
    parser.add_argument('--commit', required=True)
    args = parser.parse_args()
    if args.operation == 'select-run':
        print(select_run(json.load(sys.stdin), args.commit))
        return
    if args.directory is None or args.version is None:
        parser.error('create and verify require --directory and --version')
    expected = manifest(args.directory, args.version, args.commit)
    record = args.directory / 'release-candidate.json'
    if args.operation == 'create':
        record.write_text(json.dumps(expected, indent=2) + '\n')
    elif json.loads(record.read_text()) != expected:
        raise ValueError('tested package provenance does not match this release')
    print(f'{args.operation}: {args.version} from {args.commit}')


if __name__ == '__main__':
    main()
