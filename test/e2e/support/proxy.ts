import net from "node:net";

export type InterruptMode = "lose-reply" | "lose-write";

interface Interruption {
  mode: InterruptMode;
  bytes: number;
  tripped: () => void;
}

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
 * What the browser work behind 1.36 learned: the apply path re-reads its
 * baseline first, and that read absorbs a dead connection and recovers, so a
 * `cut` before Apply rarely lands between the read and the write. Cutting
 * until one does is a race, and a race is not a test. `interruptNextWrite`
 * is the deterministic version; `opened` is how a test tells a resent write
 * (a retry has to dial) from one that was not.
 */
export class CutProxy {
  private server: net.Server;
  private live = new Set<net.Socket>();
  private accepted = 0;
  private stopped = false;
  private armed: Interruption | null = null;
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
    const sever = () => {
      client.destroy();
      upstream.destroy();
    };
    for (const s of [client, upstream]) {
      this.live.add(s);
      s.on("close", () => this.live.delete(s));
      // A severed connection errors on the other side; that is the point
      // of cutting it, not a failure of the proxy.
      s.on("error", () => s.destroy());
    }
    client.on("end", () => upstream.end());
    upstream.on("end", () => client.end());

    // Bytes the client has sent since the server last said anything: one
    // request, near enough, since LDAP here is one operation at a time.
    let burst = 0;
    // Set once this connection's write has been let through and its reply
    // is to be lost.
    let losing: Interruption | null = null;

    client.on("data", (chunk: Buffer) => {
      const armed = this.armed;
      if (armed && !losing) {
        burst += chunk.length;
        if (burst >= armed.bytes) {
          this.armed = null;
          if (armed.mode === "lose-write") {
            // Never forwarded: the server sees, at most, the start of a
            // TLS record it can never decrypt.
            sever();
            armed.tripped();
            return;
          }
          losing = armed;
          // A reply that never comes still has to end in a cut, or the
          // test would wait on Alder's own timeout instead.
          setTimeout(() => {
            if (!client.destroyed) {
              sever();
              armed.tripped();
            }
          }, 5_000).unref();
        }
      }
      upstream.write(chunk);
    });

    upstream.on("data", (chunk: Buffer) => {
      burst = 0;
      if (losing) {
        // The server has answered, so it has processed the write. The
        // answer goes nowhere and the connection goes with it.
        const done = losing;
        losing = null;
        sever();
        done.tripped();
        return;
      }
      client.write(chunk);
    });
  }

  /**
   * Interrupts the next write through the proxy, on purpose and exactly once.
   *
   * The traffic is TLS, so the proxy cannot read it -- but it can see sizes.
   * A journey makes its write the one large thing the client sends (a long
   * value), and the proxy acts on the first request of at least `bytes`.
   * Measured on both servers: while a journey is applying, every other
   * request is under 150 bytes, and a modify carrying a 1000-character value
   * is over 1100.
   *
   * - `lose-reply`: the write reaches the server and is applied; its answer
   *   is swallowed and the connection severed. The change landed and Alder
   *   cannot know it.
   * - `lose-write`: the connection is severed before the write is
   *   forwarded. The change never arrived, and Alder cannot know that
   *   either.
   *
   * Either way Alder has sent a change it never heard back about, which is
   * the state the interrupted-write handling exists for. Resolves once the
   * interruption has happened.
   */
  interruptNextWrite(mode: InterruptMode, bytes = 800): Promise<void> {
    if (this.armed) throw new Error("an interruption is already armed");
    return new Promise((resolve) => {
      this.armed = { mode, bytes, tripped: resolve };
    });
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
