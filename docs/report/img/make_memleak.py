"""RSS-over-time chart for the memory-leak fix (parallel vs nogc).
Reads /tmp/rss_parallel.txt and /tmp/rss_nogc.txt (whitespace: elapsed_s rss_kb).
Not part of the Go project."""

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

SURFACE = "#fcfcfb"
INK_PRIMARY = "#0b0b0b"
INK_SECONDARY = "#52514e"
INK_MUTED = "#898781"
GRIDLINE = "#e1e0d9"
BASELINE = "#c3c2b7"
BLUE = "#2a78d6"
AQUA = "#1baf7a"

plt.rcParams.update({
    "font.family": "sans-serif",
    "font.sans-serif": ["Helvetica Neue", "Arial", "DejaVu Sans"],
    "text.color": INK_PRIMARY,
    "figure.facecolor": SURFACE,
    "axes.facecolor": SURFACE,
    "savefig.facecolor": SURFACE,
})


def load(path):
    t, rss = [], []
    with open(path) as f:
        for line in f:
            parts = line.split()
            if len(parts) != 2:
                continue
            t.append(float(parts[0]))
            rss.append(float(parts[1]) / 1024)  # KB -> MB
    return t, rss


t_par, rss_par = load("/tmp/rss_parallel.txt")
t_nogc, rss_nogc = load("/tmp/rss_nogc.txt")

fig, ax = plt.subplots(figsize=(7.5, 4.2), dpi=200)

ax.plot(t_par, rss_par, color=BLUE, linewidth=2, zorder=3,
        label="parallel (avant correction)")
ax.plot(t_nogc, rss_nogc, color=AQUA, linewidth=2, zorder=3,
        label="nogc (après correction)")

ax.text(t_par[-1], rss_par[-1], f"  {rss_par[-1]:,.0f} Mo",
         va="center", ha="left", fontsize=9.5, color=BLUE, fontweight="medium")
ax.text(t_nogc[-1], rss_nogc[-1], f"  {rss_nogc[-1]:.0f} Mo",
         va="top", ha="left", fontsize=9.5, color=AQUA, fontweight="medium")

ax.set_xlabel("Temps écoulé depuis le lancement (secondes)", fontsize=9.5,
               color=INK_SECONDARY)
ax.set_ylabel("Mémoire réellement occupée par le processus (Mo)",
               fontsize=9.5, color=INK_SECONDARY)
ax.yaxis.grid(True, which="major", color=GRIDLINE, linewidth=1, zorder=0)
ax.set_axisbelow(True)
for spine in ["top", "right"]:
    ax.spines[spine].set_visible(False)
ax.spines["left"].set_color(BASELINE)
ax.spines["bottom"].set_color(BASELINE)
ax.tick_params(labelsize=9, colors=INK_MUTED)
ax.set_xlim(0, max(t_par) * 1.22)

ax.legend(loc="upper left", frameon=False, fontsize=10, labelcolor=INK_SECONDARY)
ax.set_title("Mémoire du processus pendant une exécution longue (60 000 cycles, scénario large)",
             fontsize=10.5, color=INK_PRIMARY, pad=12)

fig.tight_layout()
fig.savefig("memleak.png", bbox_inches="tight")
print(f"OK: memleak.png -- parallel {rss_par[0]:.0f}->{rss_par[-1]:.0f} Mo en {t_par[-1]:.1f}s, "
      f"nogc {rss_nogc[0]:.0f}->{rss_nogc[-1]:.0f} Mo en {t_nogc[-1]:.1f}s")
