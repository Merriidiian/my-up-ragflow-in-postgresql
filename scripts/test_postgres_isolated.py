#!/usr/bin/env python3
"""Domain regression: disposable PG18.6/MySQL fixtures, internal network, no live stack.

All images and native dependencies must already exist locally. No pulls. Mounts
only this component read/write and a caller-provided disposable cache. Never
reads credentials or Compose configuration from the running installation.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import tempfile
import time
import uuid

BUILDER = 'infiniflow/github_action_runner@sha256:07712b55ee67788c147f5f3974cc5628270e99ce85edfe30c526c21a27a3371e'
POSTGRES = 'postgres@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873'
MYSQL = 'mysql@sha256:869218921e61d6c3c89820955d63cca42971f0e3e6c1e2792247bbd944ebc6e9'
ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kw):
    return subprocess.run(list(args), check=True, **kw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--module-cache', type=Path, required=True)
    parser.add_argument('--native-debs', type=Path, required=True)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    for image in (BUILDER, POSTGRES, MYSQL):
        run('docker', 'image', 'inspect', image, stdout=subprocess.DEVNULL)
    for directory in (args.module_cache, args.native_debs):
        if not directory.is_dir():
            raise SystemExit('prepared dependencies are missing')
    native_lock = json.loads((ROOT / 'native-packages.lock.json').read_text())
    packages = native_lock['packages']
    for filename, expected in packages.items():
        path = args.native_debs / filename
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise SystemExit('prepared native package checksum mismatch: ' + filename)
    install = 'sudo dpkg -i ' + shlex.join(['/vsm-debs/' + name for name in packages])
    args.scratch.mkdir(parents=True, exist_ok=True)
    args.report.mkdir(parents=True, exist_ok=True)
    name = 'vsm-core-test-' + uuid.uuid4().hex[:12]
    pg, mysql = name + '-pg', name + '-mysql'
    password = secrets.token_hex(24)
    results = {'images': {'builder': BUILDER, 'postgres': POSTGRES, 'mysql': MYSQL},
               'network': 'internal, no published ports', 'checks': [], 'cleaned_up': False}
    run('docker', 'network', 'create', '--internal', name, stdout=subprocess.DEVNULL)
    try:
        with tempfile.TemporaryDirectory(prefix='vsm-core-fixtures-') as directory:
            env = Path(directory) / 'fixtures.env'
            env.write_text('POSTGRES_PASSWORD=' + password + '\nPOSTGRES_USER=ragflow\nPOSTGRES_DB=core_test\nMYSQL_ROOT_PASSWORD=' + password + '\nMYSQL_DATABASE=core_import\n')
            env.chmod(0o600)
            for container, image, mount, extra in (
                    (pg, POSTGRES, '/var/lib/postgresql', []),
                    (mysql, MYSQL, '/var/lib/mysql', ['--mysql-native-password=ON'])):
                run('docker', 'run', '-d', '--pull', 'never', '--network', name,
                    '--name', container, '--memory', '768m', '--cpus', '2',
                    '--tmpfs', mount + ':rw,size=512m', '--env-file', str(env),
                    image, *extra, stdout=subprocess.DEVNULL)
            for container, probe in ((pg, ['pg_isready', '-U', 'ragflow', '-d', 'core_test']),
                                     (mysql, ['mysqladmin', 'ping', '--silent'])):
                deadline = time.monotonic() + 120
                while subprocess.run(['docker', 'exec', container, *probe], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
                    if time.monotonic() >= deadline:
                        raise RuntimeError('disposable fixture failed to become ready')
                    time.sleep(1)
            test_env = os.environ.copy()
            test_env.update(VSM_PG_TEST_DSN=f'postgres://ragflow:{password}@{pg}:5432/core_test?sslmode=disable',
                            VSM_MYSQL_IMPORT_TEST_DSN=f'root:{password}@tcp({mysql}:3306)/core_import?charset=utf8mb4&parseTime=true&loc=UTC',
                            VSM_MYSQL_IMPORT_TEST_REPORT='/scratch/mysql-import.json')
            base = ['docker', 'run', '--rm', '--pull', 'never', '--network', name,
                    '--memory', '7g', '--cpus', '4', '--entrypoint', '/bin/bash',
                    '-v', f'{ROOT}:/work', '-v', f'{args.module_cache.resolve()}:/go/pkg/mod:ro',
                    '-v', f'{args.native_debs.resolve()}:/vsm-debs:ro',
                    '-v', f'{args.scratch.resolve()}:/scratch', '-w', '/work',
                    '-e', 'GOPROXY=off', '-e', 'GOSUMDB=off', '-e', 'GOTOOLCHAIN=local',
                    '-e', 'GOMODCACHE=/go/pkg/mod', '-e', 'GOCACHE=/scratch/gocache',
                    '-e', 'GOFLAGS=-p=2', '-e', 'VSM_PG_TEST_DSN',
                    '-e', 'VSM_MYSQL_IMPORT_TEST_DSN', '-e', 'VSM_MYSQL_IMPORT_TEST_REPORT', BUILDER, '-lc']
            commands = [
                ('go-tests', 'test -tags cgo,static -json -count=1 -timeout 180s ./...'),
                ('integration-compile', "test -tags cgo,static,integration -run '^$' ./internal/ingestion/pipeline"),
                ('go-vet', 'vet -tags cgo,static ./...'),
                ('server-build', 'build -tags cgo,static -trimpath -o /scratch/ragflow_server ./cmd/ragflow_server.go ./cmd/deepdoc_server_ee.go'),
                ('cli-build', 'build -tags cgo,static -trimpath -o /scratch/ragflow-cli ./cmd/ragflow-cli.go'),
                ('import-build', 'build -tags cgo,static -trimpath -o /scratch/ragflow_pg_migrate ./cmd/pg_migrate'),
            ]
            for label, command in commands:
                output_path = args.report / (label + ('.jsonl' if label == 'go-tests' else '.log'))
                # Avoid leaking temporary fixture credentials even on a driver error.
                with output_path.open('w') as output:
                    process = subprocess.Popen(base + [install + ' >&2 && scripts/offline-go.sh ' + command],
                                               env=test_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                    for line in process.stdout:
                        output.write(line.replace(password, '<fixture-secret>'))
                        output.flush()
                    code = process.wait()
                results['checks'].append({'name': label, 'exit_code': code, 'log': output_path.name})
                print(label + ': exit ' + str(code), flush=True)
            for filename in ('mysql-import.json',):
                path = args.scratch / filename
                if path.exists():
                    (args.report / filename).write_text(path.read_text().replace(password, '<fixture-secret>'))
            for filename in ('ragflow_server', 'ragflow_pg_migrate', 'ragflow-cli'):
                path = args.scratch / filename
                if path.exists():
                    results[filename + '_sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
    finally:
        cleanup = subprocess.run(['docker', 'rm', '-f', '-v', pg, mysql], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode
        network_cleanup = subprocess.run(['docker', 'network', 'rm', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode
        results['cleaned_up'] = cleanup == 0 and network_cleanup == 0
        (args.report / 'isolated-run.json').write_text(json.dumps(results, indent=2) + '\n')
    return 0 if results['checks'] and all(x['exit_code'] == 0 for x in results['checks']) and results['cleaned_up'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
