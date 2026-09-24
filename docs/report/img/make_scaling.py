"""Scaling bar chart for v4 parallel (workers vs time), plus a flatgrid
reference line. Not part of the Go project; numbers hardcoded from the
hyperfine sweep run alongside this on the M4 Pro."""

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

workers = ["1", "2", "4", "8", "12"]
times = [140.6, 95.2, 78.8, 86.6, 87.1]
flatgrid_ref = 134.4

fig, ax = plt.subplots(figsize=(7.2, 4.0), dpi=200)
x = list(range(len(workers)))
ax.bar(x, times, width=0.55, color=AQUA, zorder=3)
for xi, v in zip(x, times):
    ax.text(xi, v + 3, f"{v:.0f} ms", ha="center", va="bottom",
             fontsize=10, color=INK_PRIMARY)

ax.axhline(flatgrid_ref, color=BLUE, linewidth=1.6, linestyle="--", zorder=2)
ax.text(len(workers) - 0.4, flatgrid_ref + 3,
         f"flatgrid (1 thread, référence) : {flatgrid_ref:.0f} ms",
         ha="right", va="bottom", fontsize=9.5, color=BLUE)

ax.set_xticks(x)
ax.set_xticklabels(workers, fontsize=10.5, color=INK_PRIMARY)
ax.set_xlabel("Nombre de workers (cœurs utilisés)", fontsize=9.5,
               color=INK_SECONDARY)
ax.set_ylabel("Temps d'exécution (ms) — scénario large", fontsize=9.5,
               color=INK_SECONDARY)
ax.set_ylim(0, 160)
ax.yaxis.grid(True, which="major", color=GRIDLINE, linewidth=1, zorder=0)
ax.set_axisbelow(True)
for spine in ["top", "right"]:
    ax.spines[spine].set_visible(False)
ax.spines["left"].set_color(BASELINE)
ax.spines["bottom"].set_color(BASELINE)
ax.tick_params(labelsize=9, colors=INK_MUTED)

fig.tight_layout()
fig.savefig("scaling.png", bbox_inches="tight")
print("OK: scaling.png")
