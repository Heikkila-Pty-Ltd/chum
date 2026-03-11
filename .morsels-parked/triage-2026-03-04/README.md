# Stranded Morsels Triage (2026-03-04)

This folder contains stranded morsels moved out of `.morsels/` during triage on 2026-03-04.

## Counts

- `archive_done`: 12
- `archive_invalid`: 1
- `archive_user_confirmed_resolved`: 2
- `archive_out_of_scope`: 8
- `archive_superseded_duplicate`: 34
- `likely_stale_orphan_ch_ref`: 44

Total parked: 101
Remaining active in `.morsels/`: 61

## Why moved

Files were categorized using `docs/operations/triage/stranded-morsels-triage-2026-03-04.{md,json}`.

## Restore example

Restore all parked morsels:

```bash
find .morsels-parked/triage-2026-03-04 -type f -name '*.md' -exec mv {} .morsels/ \;
```

Restore a single category:

```bash
find .morsels-parked/triage-2026-03-04/likely_stale_orphan_ch_ref -type f -name '*.md' -exec mv {} .morsels/ \;
```
