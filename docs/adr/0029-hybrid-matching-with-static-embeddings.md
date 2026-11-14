# 0029. Hybrid matching with a tech skill dictionary and static embeddings

- Status: accepted
- Date: 2026-11-14
- Deciders: maintainer
- Supersedes: the scoring part of [0023](0023-rule-based-matching.md) (hard filters, stored reasons and row-level
  security stay)

## Context and problem statement

The rules of 0023 add fixed points for title, skills, seniority, place, language, salary and recency. On a fixed
set of 40 fictional postings with graded relevance for three profiles (`internal/matching/testdata/ranking.json`)
they reach NDCG@10 0.868 and order 72 % of the pairs right. They filter out related titles ("Plattform-Ingenieur",
"Backend Engineer Go"), rank title-only hits high, ignore the years a posting asks for and cost about 124 µs per user
and job, because every job is normalized again for every user.

"Without AI" in this project means no LLMs (cost and dependence); local machine learning is fine.

## Decision drivers

- Better ranking for German and English postings, measured, not assumed.
- Runs on the CPU of a small self-hosted instance: little memory, no GPU, no extra service, no network at run time.
- Same result for the same input; every factor is shown to the user.
- Works without training data at the start.

## Considered options

1. Rules only, refined (larger skill dictionary or the ESCO taxonomy, IDF weights, continuous factors).
2. Rules plus a static embedding model (a token table, averaged; no network layers).
3. Rules plus a transformer embedding model (multilingual-e5-small, about 118 M parameters).
4. Cross-encoder or LLM re-ranking.
5. Neural person-job fit models (PJFNN, DPGNN, ConFit).

## Decision outcome

Chosen option: 2, with the refinements of 1, and learning to rank once there is feedback.

- Hard filters stay for excluded titles, place, language and salary. "No title match" is no longer a hard filter:
  the similarity decides.
- Per job, once (cached by content hash and rules version): skills from an own tech skill dictionary with German
  and English spellings (found in one pass), required vs preferred skills, years asked, and a static embedding of
  title and text.
- Score: weighted combination of text similarity (cosine of profile and job), title, IDF-weighted skill coverage,
  seniority, place, language and recency; each part is shown as a reason. Weights are fixed at first and later
  learned per user from likes, skips and applications (logistic regression).
- One "Why this score" panel; the Fit inspector goes.

Model: `sentence-transformers/static-similarity-mrl-multilingual-v1` (Apache 2.0), cut to 256 of 1024 dimensions
(Matryoshka training keeps the quality). The table is fetched at image build from a pinned revision and checked
against a SHA-256 sum, stored as float16 (about 54 MB) and read with an own Go reader (`internal/matching/embed`,
no cgo). Without the model file the similarity part is neutral and the rules still work.

Measured on the ranking set (Windows, 20 threads; one job of 2.6 KB). The tests rank with a token subset of the
model (`internal/matching/testdata/embed`, identical vectors) and keep floors for both variants:

| Variant | NDCG@10 | Pairs right | Memory | Time per job |
|---|---|---|---|---|
| Rules of 0023 | 0.868 | 0.721 | none | 124 µs per user |
| static-mrl, 256 dims, hard filters without title filter | 0.920 | 0.908 | 107 MB (float32) | 0.12 ms once |
| potion-multilingual-128M, 256 dims, same | 0.906 | 0.904 | 526 MB | 35 ms once |
| static-mrl, cosine alone (no filters) | 0.927 | 0.912 | 107 MB | 0.12 ms once |
| **Rules version 3 with the model** (weights 0.40 similarity, 0.20 title, 0.25 skills, 0.15 level; soft factors for place, language, age) | **0.963** | **0.883** | 55 MB (float16) | 0.12 ms once |
| Rules version 3 without the model (title filter stays hard) | 0.914 | 0.763 | none | |

Option 3 was not measured: option 2 already closes most of the gap, and a transformer needs ONNX Runtime (cgo) or
an extra service and 10 to 50 ms per job. Options 4 and 5 need GPUs, LLM calls or millions of labelled hires.

### Consequences

- Good, because related and German titles are found, and title-only hits no longer rank high.
- Good, because a job is analyzed once; scoring a user against it is a dot product and a few lookups.
- Good, because it runs in the Go binary on any CPU, without network at run time.
- Bad, because the image grows by about 54 MB and the worker holds the table in memory.
- Bad, because the ranking set is small and written by the project; it guards against regressions but is no proof
  of quality on real postings. The real run (#285) has to confirm it.
- Neutral: the model is third-party data (Apache 2.0), named in `deploy/embed/NOTICE`.

### ESCO

The EU taxonomy ESCO (CC BY 4.0) was checked and left out: getafjob is for IT and tech jobs, and ESCO lacks most
of their tools (no Kubernetes, Docker, Terraform, Kafka, PyTorch, React, AWS, scikit-learn or Angular; TypeScript,
PostgreSQL and DevOps are there). Importing it would add a pipeline, upkeep and generic skills ("communicate")
that add noise, for little gain on tech postings. Related job titles are covered by the embeddings.
