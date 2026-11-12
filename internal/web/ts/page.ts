// Every page, signed in or not: cross-document view transitions (@view-transition in app.css) that the browser
// skips or aborts reject their promises, which nobody else handles. A skipped transition is not an error: the page
// loads all the same. Loaded render blocking, so it listens before the first frame.

interface WithTransition {
  viewTransition?: ViewTransition | null;
}

function quiet(e: Event): void {
  const vt = (e as Event & WithTransition).viewTransition;
  if (!vt) return;
  for (const p of [vt.ready, vt.finished, vt.updateCallbackDone]) p.catch(() => {});
}

addEventListener('pageswap', quiet);
addEventListener('pagereveal', quiet);
