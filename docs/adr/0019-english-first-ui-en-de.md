# 0019. English first; UI in English and German

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

Code comments, commits (Conventional Commits), docs, issues and pull requests are in English. The UI ships English
(default) and German from message catalogs, with a test that both are complete. Texts created in the backend reach
the UI as a code plus parameters, never as prose. Logs and errors for operators are English.

### Consequences

- Good, because the public project is accessible to everyone.
- Bad, because every UI text needs two entries.
