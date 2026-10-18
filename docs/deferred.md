# Deferred work

Work decided on but not built yet. Each item gets its own PR: agree the decisions, spec it (`spec/`, `schemas/`), implement with tests, review.

## Cycles without Tarjan

`graph.components` finds the groups of tasks that block each other with a hand-written Tarjan's strongly connected components (about 65 lines). `Cycles` (for `doctor`) and `Why` (for `stuck`) both use it. Trees are small, so the groups can come from the `Path` already there instead: a task is in a cycle exactly when `Path` leads from one of its blockers back to it, and two such tasks share a group when each reaches the other. That costs a search per task, against Tarjan's single pass, which is nothing at koan's sizes. Do it with the tests as the check: `doctor`'s `cycle` findings, their order and their `ids`, and `why`'s `stuck`, must not change.
