#!/usr/bin/env python3
"""Проверка двух сервисов через HTTP, включая перезапуск Ledger."""

import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from datetime import date
from decimal import Decimal


ROOT = Path(__file__).resolve().parents[1]
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def request(base, method, path, expected=200, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"},
    )
    try:
        response = HTTP.open(req, timeout=3)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        text = response.read().decode()
        if response.code != expected:
            raise AssertionError(f"{method} {path}: got {response.code} {text}, expected {expected}")
        return json.loads(text, parse_float=Decimal)


def wait_ready(base, process):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"service exited with code {process.returncode}")
        try:
            if request(base, "GET", "/healthz") == {"status": "ok"}:
                return
        except (OSError, AssertionError):
            pass
        time.sleep(0.1)
    raise RuntimeError("service did not become ready")


def stop(process):
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=12)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise RuntimeError("service failed to shut down gracefully")


def main():
    ledger = gateway = None
    ledger_port, gateway_port = free_port(), free_port()
    while gateway_port == ledger_port:
        gateway_port = free_port()
    ledger_base = f"http://127.0.0.1:{ledger_port}"
    gateway_base = f"http://127.0.0.1:{gateway_port}"
    env = os.environ.copy()
    env.update({
        "LEDGER_ADDR": f"127.0.0.1:{ledger_port}",
        "GATEWAY_ADDR": f"127.0.0.1:{gateway_port}",
        "REDIS_DB": env.get("SMOKE_REDIS_DB", "15"),
    })
    category = f"smoke-{time.time_ns()}"
    today = date.today().isoformat()
    with tempfile.TemporaryDirectory(prefix="cashcraft-smoke-") as tmp:
        ledger_log = Path(tmp) / "ledger.log"
        gateway_log = Path(tmp) / "gateway.log"
        with ledger_log.open("w") as ledger_output, gateway_log.open("w") as gateway_output:
            def start_ledger():
                return subprocess.Popen([str(ROOT / ".bin/ledger")], cwd=ROOT, env=env, stdout=ledger_output, stderr=subprocess.STDOUT)

            try:
                ledger = start_ledger()
                wait_ready(ledger_base, ledger)
                gateway = subprocess.Popen([str(ROOT / ".bin/gateway")], cwd=ROOT, env=env, stdout=gateway_output, stderr=subprocess.STDOUT)
                wait_ready(gateway_base, gateway)
                for limit in (50, 100):
                    request(gateway_base, "POST", "/api/budgets", 201, {"category": category, "limit": limit})
                budgets = request(gateway_base, "GET", "/api/budgets")
                saved = [b for b in budgets if b["category"] == category]
                assert len(saved) == 1 and saved[0]["limit"] == 100, saved

                ids = []
                for amount in (60.10, 39.90):
                    tx = request(gateway_base, "POST", "/api/transactions", 201, {
                        "amount": amount, "category": category, "description": "smoke test", "date": today,
                    })
                    assert tx["id"] > 0, tx
                    ids.append(tx["id"])
                error = request(gateway_base, "POST", "/api/transactions", 409, {
                    "amount": 0.01, "category": category, "date": today,
                })
                assert error == {"error": "budget exceeded"}, error
                request(gateway_base, "POST", "/api/transactions", 400, {
                    "amount": 0, "category": category, "date": today,
                })
                rows = request(gateway_base, "GET", "/api/transactions")
                saved_rows = [tx for tx in rows if tx["category"] == category]
                assert [tx["id"] for tx in saved_rows] == list(reversed(ids)), saved_rows
                assert sum(tx["amount"] for tx in saved_rows) == 100, saved_rows
                print("PASS: budget upsert, transaction IDs, exact limit, HTTP 409 without insert, validation, lists")

                report_path = f"/api/reports/summary?from={today}&to={today}"
                first = request(gateway_base, "GET", report_path)
                second = request(gateway_base, "GET", report_path)
                assert first == second, (first, second)
                totals = {item["category"]: item["amount"] for item in first["by_category"]}
                assert totals[category] == 100, totals
                logs = gateway_log.read_text()
                assert "cache=miss" in logs and "cache=hit" in logs, logs
                print("PASS: report aggregation, Redis cache miss -> hit")

                stop(ledger)
                assert ledger.returncode == 0, ledger.returncode
                ledger = start_ledger()
                wait_ready(ledger_base, ledger)
                assert request(gateway_base, "GET", "/api/budgets") == budgets
                assert request(gateway_base, "GET", "/api/transactions") == rows
                print("PASS: budgets and transactions survived a standalone Ledger restart")
                stop(gateway)
                assert gateway.returncode == 0, gateway.returncode
                gateway = subprocess.Popen([str(ROOT / ".bin/gateway")], cwd=ROOT, env=env, stdout=gateway_output, stderr=subprocess.STDOUT)
                wait_ready(gateway_base, gateway)
                assert request(gateway_base, "GET", "/api/budgets") == budgets
                assert request(gateway_base, "GET", "/api/transactions") == rows
                print("PASS: Gateway and its imported Ledger instance restarted; all data retained in PostgreSQL")
                print(f"Test data retained in PostgreSQL; category: {category}")
            except Exception:
                print("Ledger log:\n" + ledger_log.read_text(), file=sys.stderr)
                print("Gateway log:\n" + gateway_log.read_text(), file=sys.stderr)
                raise
            finally:
                try:
                    stop(gateway)
                finally:
                    stop(ledger)


if __name__ == "__main__":
    main()
