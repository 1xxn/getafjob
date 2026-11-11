# Sitemap and navigation

Information architecture for Phase 8 ([ADR 0025](../adr/0025-boosted-navigation-and-live-dock.md)). Labels are
catalog keys in the UI; the English names here are the defaults.

## Navigation groups

Sidebar at 1024 px and wider, a bottom bar with five items plus "More" below. Counters come from `/events`.

| Group | Page | URL | Counter | Replaces |
|---|---|---|---|---|
| Home | Dashboard | `/` | | `/` |
| Discover | Jobs | `/jobs` | new since last visit | `/jobs` |
| | Job | `/jobs/{id}` | | `/jobs/{id}` |
| | Saved searches | `/searches` | | new |
| Apply | Live | `/live` | running | `/live/indicator` |
| | Needs input | `/needs-input` | waiting for me | parts of `/applications/{id}` |
| | Approvals | `/approvals` | waiting for approval | `/approvals` |
| | Queue | `/queue` | | new |
| Track | Applications | `/applications` | | new |
| | Application | `/applications/{id}` | | `/applications/{id}` |
| | Pipeline | `/pipeline` | | `/tracker` |
| Me | Profile | `/profile` | completeness | `/profile` |
| | Documents | `/documents` | | `/documents` |
| | Answers | `/answers` | | `/answers` |
| | Texts | `/texts` | | `/texts` |
| Settings | Search | `/settings/search` | | part of `/preferences` |
| | Automation | `/settings/automation` | | part of `/preferences` |
| | Notifications | `/settings/notifications` | | part of `/preferences` |
| | Account and security | `/settings/account` | | `/account`, `/privacy` |
| Admin | Users | `/admin/users` | | CLI |
| | Runners | `/admin/runners` | offline | CLI |
| | Sources | `/admin/sources` | failing | CLI |
| | Queue | `/admin/queue` | | CLI |
| | System | `/admin/system` | | CLI |

Admin is shown to admins only and checked on the server for every route. `/setup` stays a wizard outside the
navigation; the dashboard links to it until the profile is complete.

## URL rules

- Nouns in plural for lists, `{id}` for details, English, lower case, hyphens.
- Filters, sort and page in the query string, so every view can be bookmarked and shared between tabs.
- Tabs of a detail page as `?tab=`, rendered by the server; the default tab has no parameter.
- Actions are `POST` to a sub path (`/applications/{id}/answer`), never `GET`.
- Old URLs answer `308` to the new ones for one minor release (`/tracker`, `/preferences`, `/account`, `/privacy`).

## Page templates

| Template | Used by | Shape |
|---|---|---|
| List | Applications, Queue, Admin lists | header with primary action, filter bar, table or cards, pager |
| List with detail | Jobs, Needs input, Approvals | list left, detail right at 1200 px and wider; detail page below |
| Detail with tabs | Job, Application | header with state and next step, tabs, side panel for actions |
| Wizard | Setup, CV import | steps with progress, back, skip for now, one question group per step |
| Settings sections | Settings, Profile | section list left, one form per section, save per section |

## Shell

- Top: product name, global search (`/`), live indicator, account menu.
- Sidebar or bottom bar: groups above, active item marked with `aria-current="page"`.
- Live dock: bottom right, collapsed to a pill with the running count; expanded it shows one run with frames and a
  link to the application. Persists across navigation (`hx-preserve`).
- Toasts for results of actions, in a polite live region.

## Keyboard shortcuts

Shown with `?`. Single key shortcuts can be turned off in Settings (WCAG 2.1.4) and never fire inside inputs.

| Keys | Action |
|---|---|
| `/` | focus search |
| `g` then `h` `j` `l` `n` `a` `p` | go to Home, Jobs, Live, Needs input, Applications, Pipeline |
| `j` `k` | next, previous item in a list |
| `Enter` | open item |
| `a` `s` `x` | apply, save, skip the selected job |
| `Esc` | close detail panel or dialog |
