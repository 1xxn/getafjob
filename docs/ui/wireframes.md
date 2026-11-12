# Wireframes (low fidelity)

Structure only: no colors, no final texts. Widths: desktop 1440 px, phone 375 px. See [sitemap](sitemap.md).

## Shell, desktop

```
+--------------------------------------------------------------------------------------+
| getafjob        [ Search jobs, applications ...  / ]          (o 2 live)   [Max v]   |
+--------------+-----------------------------------------------------------------------+
| Home         |  #main                                                                |
| DISCOVER     |                                                                       |
|  Jobs     12 |                                                                       |
|  Searches    |                                                                       |
| APPLY        |                                                                       |
|  Live      2 |                                                                       |
|  Needs in. 3 |                                                                       |
|  Approvals 1 |                                                                       |
|  Queue       |                                                                       |
| TRACK        |                                                                       |
|  Applications|                                                    +-----------------+|
|  Pipeline    |                                                    | Live dock   [_] ||
| ME           |                                                    | [ frames      ] ||
|  Profile 80% |                                                    | Acme, Backend   ||
|  ...         |                                                    | Filling, 4 of 9 ||
| SETTINGS     |                                                    | [Open] [Next >] ||
| ADMIN        |                                                    +-----------------+|
+--------------+-----------------------------------------------------------------------+
```

## Shell, phone

```
+-----------------------------+
| getafjob   (/)  (o 2)  [M]  |
+-----------------------------+
| #main                       |
|                             |
|                             |
|               ( o 2 live )  |  <- dock collapsed to a pill
+-----------------------------+
| Home Jobs Apply Track More  |
+-----------------------------+
```

## Dashboard `/`

```
| Good morning, Max                                                    [Find jobs]  |
+------------------+------------------+------------------+------------------------+
| Needs you  3     | Running  2       | New jobs  12     | Sent this week  5      |
| [Answer next]    | [Watch]          | [Review]         | [Applications]         |
+------------------+------------------+------------------+------------------------+
| Needs you                              | Latest activity                         |
|  Acme, Backend  question: notice  [>]  |  10:12 Beta GmbH submitted, confirmed   |
|  Beta, SRE      approval          [>]  |  10:05 Gamma started                    |
|  ...                                   |  ...                                    |
+----------------------------------------+-----------------------------------------+
| Profile 80%: add a language for your CV  [Complete]                              |
```

## Jobs `/jobs` (list with detail)

```
| Jobs                                         [Import URL] [Crawl now]            |
| [Search......] [Source v] [Remote v] [Fit >= v] [Status v]     Sort [Fit v]      |
+-------------------------------------+--------------------------------------------+
| > Backend Engineer   Acme   92  new |  Backend Engineer, Acme                    |
|   SRE                Beta   85      |  Berlin or remote . found 2 h ago . Greenh. |
|   Platform Eng.      Gamma  80  ok  |  Fit 92: Go, Postgres, Kubernetes   [Why]  |
|   ...                               |  CV [Backend EN v]                         |
|                                     |  [Apply now]  [Save]  [Skip]  [Block co.]  |
| < 1 2 3 >                           |  Description ...                           |
+-------------------------------------+--------------------------------------------+
```

Below 1200 px the detail opens as its own page `/jobs/{id}`.

## Application `/applications/{id}` (detail with tabs)

```
| < Applications                                                                   |
| Backend Engineer, Acme                        Waiting for your answer             |
| Next step: answer 1 question, then getafjob continues.   [Answer and continue]   |
+--------------------------------------------------------+-------------------------+
| [Overview] [Fields] [Evidence] [Timeline]              | Live                    |
|                                                        | [ frames            ]   |
|  Question: Notice period?                              | [Take over]             |
|  ( ) 1 month (x) 3 months ( ) other                    |                         |
|  [Answer and continue]   [Park]                        | Actions                 |
|                                                        | Open posting            |
|                                                        | Withdraw                |
+--------------------------------------------------------+-------------------------+
```

Fields tab: label, value, source chip (`profile`, `answer`, `CV`, `you`), details on demand.

## Needs input `/needs-input` (list with detail)

```
| Needs input  3                                                                   |
+-------------------------------------+--------------------------------------------+
| > Acme, Backend   1 question   2 m  |  Acme, Backend Engineer                    |
|   Beta, SRE       captcha      5 m  |  Notice period?                            |
|   Gamma, Platform approval     1 h  |  ( ) 1 month (x) 3 months                  |
|                                     |  [ ] Save as answer for next time          |
|                                     |  [Answer and continue]                     |
+-------------------------------------+--------------------------------------------+
```

Captcha items open the live view with takeover; approvals link to the approval review.

## Pipeline `/pipeline`

```
| Pipeline                                          [Add external application]     |
+---------------+---------------+---------------+---------------+-----------------+
| Applied   4   | Interview 2   | Offer  1      | Rejected  3   | Withdrawn  0    |
| [card]        | [card]        | [card]        | [card]        |                 |
| [card]        | [card]        |               |               |                 |
+---------------+---------------+---------------+---------------+-----------------+
```

Cards move by drag and drop or by a stage menu on each card (keyboard). Phone: one column with a stage switcher.

## Settings `/settings/automation` (settings sections)

```
| Settings                                                                         |
+----------------+-----------------------------------------------------------------+
| Search         | Automation                                                      |
| > Automation   | How far getafjob goes on its own                                |
| Notifications  | ( ) Ask before every application                                |
| Account        |     You approve each one. Nothing is sent without you.          |
|                | (x) Send when all fields have a source                          |
|                |     ...                                                         |
|                | [Save]                                                          |
+----------------+-----------------------------------------------------------------+
```

## Setup `/setup` (wizard)

```
| Setup                                                                            |
| Check your details                                         Step 2 of 5           |
| [==========----------------]                                                     |
| (1) CV  (2) Details  (3) Search  (4) Automation  (5) Matches                     |
| Contact, work facts, skills; positions found in the CV                           |
| [< Back]                     [Finish later]   [Skip]   [Save and continue]       |
```

Steps: CV, details found by the import, search, automation level, first matches (live while the crawl and the
rating run). `/setup` resumes at the first step that still lacks something: a CV, a field forms ask for, a job
title. Skip moves on without saving; Finish later goes to the dashboard, which links back until the profile is
complete.
