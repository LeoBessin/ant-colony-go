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

workers = ["1", "12"]
times = [140.6, 87.1]
colors = [BLUE, AQUA]  # 1 cœur = avant (bleu), 12 cœurs = après (vert)

fig, ax = plt.subplots(figsize=(5.4, 3.0), dpi=150)
x = list(range(len(workers)))
ax.bar(x, times, width=0.55, color=colors, zorder=3)
for xi, v in zip(x, times):
    ax.text(xi, v + 3, f"{v:.0f} ms", ha="center", va="bottom",
             fontsize=10, color=INK_PRIMARY)

legend_handles = [
    plt.Rectangle((0, 0), 1, 1, color=BLUE, label="1 cœur (avant)"),
    plt.Rectangle((0, 0), 1, 1, color=AQUA, label="Plusieurs cœurs (après)"),
]
ax.legend(handles=legend_handles, loc="upper right", frameon=False,
          fontsize=9.5, labelcolor=INK_SECONDARY)

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
