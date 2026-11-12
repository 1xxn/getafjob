// ts/page.ts
function quiet(e) {
  const vt = e.viewTransition;
  if (!vt) return;
  for (const p of [vt.ready, vt.finished, vt.updateCallbackDone]) p.catch(() => {
  });
}
addEventListener("pageswap", quiet);
addEventListener("pagereveal", quiet);
