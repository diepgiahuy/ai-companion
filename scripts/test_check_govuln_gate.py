#!/usr/bin/env python3
from __future__ import annotations

import io
import unittest

import check_govuln_gate as gate


def stream(go_version="go1.26.5", scan_level="symbol", findings=()):
    parts = [f'{{"config":{{"protocol_version":"1.0.0","scanner_name":"govulncheck","go_version":"{go_version}","scan_level":"{scan_level}"}}}}']
    parts.extend(findings)
    return io.StringIO("\n".join(parts))


def finding(osv, module, fixed, function="Vulnerable"):
    return (
        '{"finding":{"osv":"%s","fixed_version":"%s",'
        '"trace":[{"module":"%s","package":"example/pkg","function":"%s"}]}}'
        % (osv, fixed, module, function)
    )


class GateTests(unittest.TestCase):
    def test_allows_only_current_unreleased_stdlib_fix(self):
        go_version, allowed, blocked = gate.classify(
            stream(findings=[finding("GO-TEST-1", "stdlib", "v1.26.6")])
        )
        self.assertEqual(go_version, "go1.26.5")
        self.assertEqual(len(allowed), 1)
        self.assertEqual(blocked, [])

    def test_toolchain_style_fixed_version_does_not_accidentally_match(self):
        _, allowed, blocked = gate.classify(
            stream(findings=[finding("GO-TEST-PREFIX", "stdlib", "go1.26.6")])
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_blocks_module_vulnerability(self):
        _, allowed, blocked = gate.classify(
            stream(findings=[finding("GO-TEST-2", "example.com/dependency", "v1.2.3")])
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_blocks_no_fix(self):
        _, allowed, blocked = gate.classify(
            stream(findings=[finding("GO-TEST-3", "stdlib", "")])
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_exception_stops_on_future_go_version(self):
        _, allowed, blocked = gate.classify(
            stream(go_version="go1.26.6", findings=[finding("GO-TEST-4", "stdlib", "v1.26.6")])
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_ignores_non_symbol_intermediate_finding(self):
        _, allowed, blocked = gate.classify(
            stream(findings=[finding("GO-TEST-5", "example.com/dependency", "v1.2.3", function="")])
        )
        self.assertEqual(allowed, [])
        self.assertEqual(blocked, [])

    def test_requires_symbol_scan_level(self):
        with self.assertRaises(ValueError):
            gate.classify(stream(scan_level="package"))


if __name__ == "__main__":
    unittest.main()
