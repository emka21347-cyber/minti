# MINTI Brand & Visual Identity

## Color palette

| Role | Name | Hex | ANSI 256 | Usage |
|------|------|-----|----------|-------|
| Primary accent | Mint green | `#00d787` | `\e[38;5;42m` | Logo, headings, active state, progress bars |
| Secondary accent | Bright cyan | `#5fd7ff` | `\e[38;5;81m` | Node indicators, agent status, links |
| Divider | Mid grey | `#8a8a8a` | `\e[38;5;245m` | Horizontal rules, separators |
| Label | Light grey | `#bcbcbc` | `\e[38;5;250m` | Field labels, secondary text |
| Value | Near-white | `#eeeeee` | `\e[38;5;255m` | Field values, body text |
| Background | Deep black | `#0a0a0a` | terminal default | All backgrounds |
| Surface | Dark surface | `#1a1a1a` | — | Subtle grid, card backgrounds |

Palette rule: mint green is reserved for **single-element focus** (one logo, one progress bar, one selected item). Never use it for body text. Cyan is for **enumerable things** — nodes, agents, peers. Greys carry everything else.

## Typography

| Context | Typeface | Notes |
|---------|----------|-------|
| Terminal / console | JetBrains Mono, Fira Code, or system monospace | Any ligature-capable mono works |
| GRUB boot menu | DejaVu Sans Bold 18pt (`.pf2`) | Generated via `grub-mkfont` |
| SVG wordmark | Path-only (no external font dependency) | Outlines embedded in `assets/logo.svg` |

## Logo

The MINTI logo is a 7-row node-graph rendered in box-drawing glyphs. The geometry represents a 3×3 mesh of Clan peers.

```
      ●───●───●
     ╱ │   │   ╲
    ●──┼───●───┼──●
    │  │   ║   │  │
    ●──┼───●───┼──●
     ╲ │   │   ╱
      ●───●───●

      M I N T I
      ─────────
      Open agents.
      Open weights.
      Clan-aware.
```

- Nodes (`●`) in bright cyan `#5fd7ff`
- Edges (`─ │ ╱ ╲ ┼ ║`) in bright cyan
- Wordmark `M I N T I` in mint green `#00d787` bold
- Rule `─────────` in mid grey `#8a8a8a`
- Tagline in dim/grey

## Source files

| File | Description |
|------|-------------|
| `assets/logo.svg` | Vector logo, renders cleanly 16px–2560px |
| `assets/wallpaper-3840x2160.svg` | Master wallpaper (SVG source) |
| `assets/wallpaper-2560x1440.png` | Rasterized for desktops |
| `assets/wallpaper-1920x1080.png` | Rasterized for 1080p / GRUB background |
| `assets/plymouth/minti/` | Plymouth boot splash theme |
| `assets/grub-theme/minti/` | GRUB2 boot menu theme |
| `branding/minti-fetch` | neofetch-style console info script |

## Usage rules

1. **Don't recolor the logo.** Mint green + cyan on black only. No white versions, no inverted versions.
2. **Minimum clear space.** At least one logo-width of clear space on all sides.
3. **Tagline is optional** below the wordmark. Never abbreviate or reorder the three lines.
4. **No gradients.** Flat color only — the mesh evokes structure, not decoration.
5. **ASCII fallback.** When ANSI is unavailable, the plain-text logo must still be readable.
