# MINTI ▚

**A household's old laptops, joined into one trust group, with no server in
the middle.**

MINTI is a bootable Debian-based desktop you flash onto a laptop that is
otherwise on its way to being e-waste. Each machine is given a name from the
sky — a *star* — and stars on the same network find each other, prove who
they are, elect one of themselves to co-ordinate, and pass work to whichever
machine is actually able to do it. A 4 GB netbook with no models on it can
ask a question and have it answered by the strongest machine in the house.
Nothing is sent to a company. There is no account, no broker and no cloud
endpoint anywhere in the path.

It is a **research vehicle first**. The question it exists to answer is
narrow and falsifiable:

> **Can a household's old machines form a trust group with no server —
> and can the weakest of them earn a real role in it?**

If the 4 GB machines never become useful, that is a finding, and the project
says so rather than quietly redefining useful.

---

## How it works

Five moving parts, in the order a machine meets them.

**1. Identity.** Every machine gets a stable star name and a keypair on
first boot. A star is a member of exactly one *clan*. Membership is granted
by an invite that is single-use, and every message between members carries
an HMAC signed with the clan key over a pinned TLS certificate. There is no
password and no central directory.

**2. Discovery.** Stars announce themselves over mDNS on the local network
and keep a registry of the peers they have seen, with the address each peer
advertises for itself. Nothing outside the LAN is contacted to do this.

**3. Co-ordination.** Stars run a small leader election with monotonic terms
and short leases: one star holds the lease and heartbeats, the others
follow, and if it goes quiet another takes over. Eligibility is deliberately
about capability, not seniority — a star with no model resident cannot hold
the lease, because it could not do the job it would be co-ordinating.

**4. Routing.** A request names a *model*, not a machine. The clan resolves
which star can serve that model and forwards the whole request there — never
a sharded tensor, never a partial layer. Whole-request routing is the right
physics for a house full of mismatched hardware on domestic WiFi: splitting
a model across four weak laptops is slower than running it on the best one.
Every reply is stamped with the star that served it, so a result can always
be traced to the machine that produced it.

**5. Consent.** A star that serves other people's requests runs the resident
agent's change-class tools **off** unless its owner turns them on. Lending
the house your GPU should not mean lending it your filesystem. *(Adopted
September 2026 and implemented on the working line — this is one of the
things not yet in the snapshot below.)*

The daemon that does all of this is `cland`. The desktop it ships on is a
live-build Debian image with XFCE, an installer, and a dashboard that
refuses to display a number it did not measure.

---

## The rules the project actually holds itself to

These are not aspirations; they are enforced in review and in code.

| Rule | Meaning |
|---|---|
| **Never invent a number** | Any figure on screen or in a document traces to a measurement. A plausible-looking number with no source is treated as a defect, not a placeholder. |
| **A moving pixel requires a measurement** | If something animates to suggest activity, it is driven by real activity or it does not move. |
| **Nothing is called a quorum** | Borrowed distributed-systems vocabulary must mean what it means, or a plainer word is used. |
| **The model is chosen by measurement** | The extraction model in the pipeline was picked by benchmarking candidates against hand labels, not by reputation. |

---

## Hardware

The target is deliberately the hardware nobody else serves.

| Machine | Status |
|---|---|
| Lenovo Y-series, 2017–18 | Works |
| MacBook 2013–2015 | Prime target; needs EFI boot and the `wl` driver |
| MacBook 2016–2017 | Should work |
| MacBook 2018–2020 (T2) | Stretch; SSD and keyboard sit behind the T2 |
| Apple Silicon | Will not work — different architecture |

The floor is 4 GB of RAM and a GPU old enough that modern runtimes decline
it. That constraint is the point: comparable projects that route AI across
machines require recent GPUs, which excludes exactly the hardware this is
for.

---

## Status — read this before judging the tree

**This public branch is a snapshot, not the current line.** Active
development happens on an unpublished branch and is deliberately held back
until the first real workload runs across the fleet end to end. That is the
project's own release criterion, written down in advance so it cannot be
quietly moved: a genuine job, running on real machines, measured — then
publish.

So: the substrate here is real and readable, the polish is not finished, and
the newest work is not here yet. If you are reading this because someone
sent you the link, that is the honest position.

---

## Layout

| Path | What it is |
|---|---|
| [`PRD.md`](PRD.md) | Product requirements — every locked decision, each with the reason it was taken |
| [`STATUS.md`](STATUS.md) | Session log, newest first |
| [`archive/cland/`](archive/cland/) | The clan daemon — identity, discovery, membership, election, routing, transport |
| [`archive/`](archive/) | The rest of the Go stack: runtime adapter, workspace, MCP servers |
| [`iso/`](iso/) | The live-build image: package lists, theming, installer, boot |
| [`docs/`](docs/) | Plans, measurements and handovers |
| [`CHANGELOG.md`](CHANGELOG.md) | Flashable versions |

> **`archive/` is a misleading name and it is being fixed.** It is not dead
> code — it holds the daemon and every other Go binary that ships on the
> image. The directory was renamed during a reorganisation and the rename
> back has not reached this branch yet.

---

## Licence

[MIT](LICENSE). The project is open source permanently — that is a recorded
decision, not a default, and it is not up for revisiting.
