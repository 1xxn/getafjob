# 0011. Browser automation with go-rod

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

The runner fills real application forms in Chromium. The library must handle the hardest known form quirks, run in
Go only and keep the browser sandbox on. Phase 0 spike (#9) tested go-rod v0.116.2 against the fixtures inside the
runner image (headless-shell 157, seccomp profile, non-root, read-only rootfs).

## Considered options

1. go-rod
2. chromedp
3. playwright-go (needs the Node-based Playwright driver)
4. Raw CDP client of our own

## Spike results

| Case | Result | How |
|---|---|---|
| Header button with the same name as the submit (`greenhouse_header_submit`) | passed | extraction marks the form of each button; submit only with the button inside the form |
| react-select, async suggestions rendered twice (`greenhouse_select`) | passed | click at the element's position (placeholder covers the input), type, pick `[role=option]` with retry, verify by re-extraction |
| Listbox buttons without role (`workday_listbox`) | passed | reported as combobox, not as button |
| Drop zone that ignores directly set files (`dropzone`) | passed | `Page.HandleFileDialog` intercepts the zone's own file chooser |
| Lazy iframe below the fold (`lazy_embed`) | passed | scroll, then the frame becomes readable |
| Popup that closes itself (`popup_posting`) | passed | `WaitOpen`; the run continues in the original tab |
| Pressed toggle (`ashby_yesno`) | passed | `aria-pressed` reported, so the engine never clicks it off |
| Shadow DOM inside an iframe (`apply` → `step1`/`step2`) | passed | own resolver walks open shadow roots (CSS queries do not pierce them) |
| Out-of-process iframe (other site) | passed, with a workaround | see findings |

Each case runs in 1 to 3 s; the suite is stable over repeated shuffled runs.

## Findings

- **Unsafe launcher defaults:** go-rod adds `--no-sandbox` inside containers, turns site isolation off and unpacks
  the `leakless` helper. `internal/runner/browser` overrides all three and rejects sandbox-weakening flags.
- **Out-of-process iframes:** `Element.Frame` panics on them (nil pointer in `getJSCtxID`) and
  `Browser.PageFromTarget` refuses iframe targets. Workaround: attach to the iframe target (its ID equals the frame
  ID) with `Target.attachToTarget` and talk CDP through a small `proto.Client`; keyboard input goes to the top
  page after focusing the element. Panics inside go-rod are recovered per frame.
- **Shadow DOM:** element resolution uses our own `data-gaj` IDs and a resolver that walks open shadow roots.
- **Covered inputs:** go-rod's `Click` refuses covered elements; clicking at the element's position (as a person
  would) is needed for some widgets.
- **Implicit retries:** element queries retry until the context deadline by default; lookups use
  `NotFoundSleeper` and explicit timeouts.

## Decision outcome

Chosen option: "go-rod", wrapped by our own thin layer (`internal/runner/browser`) for the launch policy, frame
handling (incl. out-of-process iframes), element resolution and extraction. It covers every spike case, is pure Go
and keeps the sandbox on. chromedp offers no high-level out-of-process iframe support either and needs more code
per action; playwright-go would bring back a Node runtime; a raw CDP client would duplicate what go-rod already
does well.

### Consequences

- Good, because the hardest known quirks work today, inside the hardened runner image.
- Bad, because out-of-process iframes need our own CDP path for actions (click, upload), planned in #24.
- Follow-up: the `Element.Frame` panic is reported upstream in [go-rod/rod#1234](https://github.com/go-rod/rod/issues/1234#issuecomment-6682040439).
