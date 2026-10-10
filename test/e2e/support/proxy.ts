import net from "node:net";

/**
 * A TCP proxy between Alder and a directory that a test can sever.
 *
 * Every journey connects through one, passing traffic straight through, so
 * the path the interrupted-write journey depends on is exercised by every
 * other journey too, rather than being machinery that is only switched on
 * for the one test that needs it. The same idea as cutProxy in
 * test/conformance/reconnect_test.go, reachable from a browser session.
 *
 * TLS passes through untouched -- it is a byte pipe, not a terminator -- so
 * Alder still verifies the harness certificate end to end. The certificate
 * is issued for localhost and the proxy listens on 127.0.0.1, which is why
 * the connection form's "Certificate name override" is set to localhost.
 *
 * What the browser work behind 1.36 learned, and why `cut` is a method
 * rather than a one-shot: the apply path re-reads its baseline first, and
 * that read absorbs a dead connection and recovers, so a single cut rarely
 * lands between the read and the write. A test reaching for the ambiguous
 * case cuts repeatedly; `opened` is how it tells a resent write (a retry has
 * to dial) from one that simply landed first.
 */
export class CutProxy {
  private server: net.Server;
  private live = new Set<net.Socket>();
  private accepted = 0;
  private stopped = false;
  port = 0;

  private constructor(private backendPort: number, private backendHost: string) {
    this.server = net.createServer((client) => this.handle(client));
  }

  static async start(backendPort: number, backendHost = "127.0.0.1"): Promise<CutProxy> {
    const p = new CutProxy(backendPort, backendHost);
    await new Promise<void>((resolve, reject) => {
      p.server.once("error", reject);
      p.server.listen(0, "127.0.0.1", () => resolve());
    });
    p.port = (p.server.address() as net.AddressInfo).port;
    return p;
  }

  private handle(client: net.Socket) {
    if (this.stopped) {
      client.destroy();
      return;
    }
    const upstream = net.connect(this.backendPort, this.backendHost);
    this.accepted++;
    for (const s of [client, upstream]) {
      this.live.add(s);
      s.on("close", () => this.live.delete(s));
      // A severed connection errors on the other side; that is the point
      // of cutting it, not a failure of the proxy.
      s.on("error", () => s.destroy());
    }
    client.pipe(upstream);
    upstream.pipe(client);
  }

  /** How many connections have been made through the proxy so far. */
  opened(): number {
    return this.accepted;
  }

  /** Severs every connection passing through, and keeps accepting new ones. */
  cut(): number {
    const n = this.live.size;
    for (const s of this.live) s.destroy();
    this.live.clear();
    return n;
  }

  /** Severs everything and stops listening: a directory that stays gone. */
  async stop(): Promise<void> {
    this.stopped = true;
    this.cut();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}
