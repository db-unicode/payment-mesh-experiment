# Evidence directory

`raw/` is populated by Locust and runner commands and is intentionally ignored by Git. Store `metadata.json` with the commit, platform, runtime versions, scenario configuration and timestamp. `mise run report` converts whatever evidence exists into `report.md`; missing cluster evidence is called out as a limitation.
