// Measures how far this machine's clock is ahead of anchor.slimepay.com by
// watching for the moment the server's Date header ticks to the next second.
const URL = "https://anchor.slimepay.com/.well-known/stellar.toml";

async function sample() {
  const before = Date.now();
  const res = await fetch(URL, { method: "HEAD", cache: "no-store" });
  const after = Date.now();
  return { server: Date.parse(res.headers.get("date")), local: (before + after) / 2, rtt: after - before };
}

(async () => {
  const skews = [];
  let prev = await sample();
  const end = Date.now() + 12000;
  while (Date.now() < end) {
    const cur = await sample();
    if (cur.server > prev.server && cur.rtt < 1500 && prev.rtt < 1500) {
      // The tick happened between prev and cur; the server crossed cur.server
      // at roughly the midpoint of the two samples.
      const crossing = (prev.local + cur.local) / 2;
      skews.push(crossing - cur.server);
    }
    prev = cur;
  }
  skews.sort((a, b) => a - b);
  const median = skews[Math.floor(skews.length / 2)] ?? 0;
  console.log(JSON.stringify({ samples: skews.length, skews: skews.map(Math.round), medianAheadMs: Math.round(median) }));
})();
