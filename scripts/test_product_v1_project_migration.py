#!/usr/bin/env python3
from __future__ import annotations

import unittest

import migrate_product_v1_project_plan as migration


class StaticPlanTests(unittest.TestCase):
    def test_static_plan_is_valid(self) -> None:
        migration.validate_static_plan()

    def test_new_product_hierarchy_has_one_parent_per_child(self) -> None:
        parents: dict[int, int] = {}
        for parent, children in migration.PARENT_CHILDREN.items():
            for child in children:
                self.assertNotIn(child, parents)
                parents[child] = parent
        self.assertEqual(parents[196], 195)
        self.assertEqual(parents[200], 199)
        self.assertEqual(parents[202], 201)
        self.assertEqual(parents[204], 203)
        self.assertEqual(parents[207], 206)
        self.assertEqual(parents[208], 2)

    def test_release_gate_has_no_static_blocker_fan_in(self) -> None:
        self.assertNotIn(208, migration.DEPENDENCIES_ADD)

    def test_settings_dependency_chain_and_wave_are_explicit(self) -> None:
        self.assertEqual(migration.DEPENDENCIES_ADD[197], (196,))
        self.assertEqual(migration.DEPENDENCIES_ADD[198], (197,))
        self.assertEqual(migration.PROJECT_META[197]["Wave"], "W2 Product")
        self.assertEqual(migration.PROJECT_META[198]["Wave"], "W2 Product")

    def test_stale_provider_to_model_edge_is_the_only_forced_removal(self) -> None:
        self.assertEqual(migration.DEPENDENCIES_REMOVE, {23: (18,)})

    def test_planning_values_use_only_existing_project_options(self) -> None:
        for fields in migration.PROJECT_META.values():
            for field, value in fields.items():
                self.assertIn(value, migration.REQUIRED_PROJECT_OPTIONS[field])

    def test_final_gate_maps_to_existing_project_vocabulary(self) -> None:
        self.assertEqual(migration.PROJECT_META[208]["Priority"], "P0")
        self.assertEqual(migration.PROJECT_META[208]["Wave"], "W2 Product")
        self.assertEqual(migration.PROJECT_META[208]["Required Evidence"], "T3 Physical HIL")


class ParsingTests(unittest.TestCase):
    def test_parent_number_parses_rest_parent_url(self) -> None:
        self.assertEqual(
            migration.parent_number(
                {"parent_issue_url": "https://api.github.com/repos/diepgiahuy/ai-companion/issues/195"}
            ),
            195,
        )

    def test_parent_number_accepts_no_parent(self) -> None:
        self.assertIsNone(migration.parent_number({"parent_issue_url": None}))

    def test_graph_action_text_is_stable_and_human_readable(self) -> None:
        self.assertEqual(
            migration.GraphAction("add_blocker", 197, 196).text(),
            "blocked_by #197 <- #196",
        )


if __name__ == "__main__":
    unittest.main()
