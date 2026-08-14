#!/usr/bin/env python3
import unittest

import check_go_vulns as gate


class GoVulnGateTest(unittest.TestCase):
    def test_allows_only_go1265_stdlib_fixed_in_1266(self):
        allowed, blocked = gate.evaluate(
            "go1.26.5",
            [{
                "osv": "GO-TEST-0001",
                "fixed_version": "go1.26.6",
                "trace": [{"module": "stdlib", "package": "net/http", "function": "Serve"}],
            }],
        )
        self.assertEqual(len(allowed), 1)
        self.assertEqual(blocked, [])

    def test_blocks_dependency_vulnerability(self):
        allowed, blocked = gate.evaluate(
            "go1.26.5",
            [{
                "osv": "GO-TEST-0002",
                "fixed_version": "v1.2.3",
                "trace": [{"module": "example.com/dependency", "package": "example.com/dependency/x", "function": "Bad"}],
            }],
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_exception_expires_on_new_toolchain(self):
        allowed, blocked = gate.evaluate(
            "go1.26.6",
            [{
                "osv": "GO-TEST-0003",
                "fixed_version": "go1.26.6",
                "trace": [{"module": "stdlib", "package": "crypto/x509", "function": "ParseCertificate"}],
            }],
        )
        self.assertEqual(allowed, [])
        self.assertEqual(len(blocked), 1)

    def test_only_symbol_findings_count_as_reachable(self):
        messages = [
            {"config": {"go_version": "go1.26.5"}},
            {"finding": {"osv": "GO-TEST-0004", "fixed_version": "go1.26.6", "trace": [{"module": "stdlib"}]}},
            {"finding": {"osv": "GO-TEST-0004", "fixed_version": "go1.26.6", "trace": [{"module": "stdlib", "package": "net/http", "function": "Serve"}]}},
        ]
        scanner_go, findings = gate.reachable_findings(messages)
        self.assertEqual(scanner_go, "go1.26.5")
        self.assertEqual(len(findings), 1)


if __name__ == "__main__":
    unittest.main()
