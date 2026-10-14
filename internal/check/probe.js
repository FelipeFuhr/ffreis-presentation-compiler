// Deterministic slide checks, evaluated in the page after render.
//
// Everything here is measured, not guessed: the browser has already done
// layout, so "does this text fit" is a number rather than an estimate. That is
// the whole reason the compiler renders HTML instead of placing boxes.
//
// Returns [{slide, rule, element, detail}]. Severity is decided in Go.
(() => {
  const TOL = __TOL_PX__;     // px; see Options.TolerancePx in check.go
  const MIN_FONT_PX = __MIN_FONT_PX__;
  const OVERLAP_MIN_AREA_PX = __OVERLAP_MIN_AREA_PX__; // px^2; see Options.OverlapMinAreaPx
  const FONTS = __FONTS__;
  // A slide with more leaf text blocks than this is pathological (almost
  // certainly a generated diagram with one span per glyph, not authored
  // content); the overlap rule skips such a slide rather than running an
  // O(n^2) pairwise comparison that would hang the check instead of
  // reporting on it.
  const MAX_OVERLAP_LEAVES = 400;
  const findings = [];
  const add = (slide, rule, element, detail) =>
    findings.push({ slide, rule, element, detail });

  // A short, stable identifier for an element, for the report.
  const describe = (el) => {
    const cls = (el.className && typeof el.className === "string")
      ? "." + el.className.trim().split(/\s+/).slice(0, 2).join(".")
      : "";
    const text = (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
    return `${el.tagName.toLowerCase()}${cls}${text ? ` "${text}…"` : ""}`;
  };

  const parseRGB = (value) => {
    const m = /rgba?\(([^)]+)\)/.exec(value || "");
    if (!m) return null;
    const parts = m[1].split(",").map((p) => parseFloat(p));
    return { r: parts[0], g: parts[1], b: parts[2], a: parts.length > 3 ? parts[3] : 1 };
  };

  // Effective background: the first ancestor with a non-transparent colour.
  // Falls back to white, which is what the slide paints.
  const backgroundOf = (el) => {
    for (let node = el; node && node !== document.documentElement; node = node.parentElement) {
      const bg = parseRGB(getComputedStyle(node).backgroundColor);
      if (bg && bg.a > 0.1) return bg;
    }
    return { r: 255, g: 255, b: 255, a: 1 };
  };

  const luminance = ({ r, g, b }) => {
    const channel = (c) => {
      const s = c / 255;
      return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
  };

  const contrastRatio = (fg, bg) => {
    const a = luminance(fg) + 0.05;
    const b = luminance(bg) + 0.05;
    return a > b ? a / b : b / a;
  };

  const isVisible = (el, style) =>
    style.display !== "none" &&
    style.visibility !== "hidden" &&
    parseFloat(style.opacity || "1") > 0.05 &&
    el.getBoundingClientRect().width > 0 &&
    el.getBoundingClientRect().height > 0;

  // Direct text content only, so a wrapper is not blamed for its children.
  const ownText = (el) =>
    [...el.childNodes]
      .filter((n) => n.nodeType === Node.TEXT_NODE)
      .map((n) => n.textContent.trim())
      .join(" ")
      .trim();

  // Real intersecting area of two rects, in px^2. Zero when they merely
  // touch or do not overlap, so a shared edge never reads as a collision.
  const overlapArea = (a, b) => {
    const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
    const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
    return w > 0 && h > 0 ? w * h : 0;
  };

  for (const slide of document.querySelectorAll("[data-slide]")) {
    const n = Number(slide.dataset.slide) || 0;
    const frame = slide.getBoundingClientRect();

    // 1. The slide frame itself must not have more content than it can show.
    if (slide.scrollHeight > slide.clientHeight + TOL) {
      add(n, "overflow", "slide", `content is ${Math.round(slide.scrollHeight - slide.clientHeight)}px taller than the slide`);
    }
    if (slide.scrollWidth > slide.clientWidth + TOL) {
      add(n, "overflow", "slide", `content is ${Math.round(slide.scrollWidth - slide.clientWidth)}px wider than the slide`);
    }

    // 2. Any box the theme marked as measurable — the clipping boxes.
    for (const box of slide.querySelectorAll("[data-measure]")) {
      if (box.scrollHeight > box.clientHeight + TOL) {
        add(n, "overflow", describe(box), `clipped: ${Math.round(box.scrollHeight - box.clientHeight)}px of content does not fit`);
      }
    }

    for (const el of slide.querySelectorAll("*")) {
      const style = getComputedStyle(el);
      if (!isVisible(el, style)) continue;
      const rect = el.getBoundingClientRect();

      // 3. Nothing may escape the slide, since the slide is the printed page.
      const over = [
        rect.left < frame.left - TOL ? "left" : null,
        rect.right > frame.right + TOL ? "right" : null,
        rect.top < frame.top - TOL ? "top" : null,
        rect.bottom > frame.bottom + TOL ? "bottom" : null,
      ].filter(Boolean);
      if (over.length) {
        add(n, "out-of-bounds", describe(el), `escapes the slide on the ${over.join(", ")}`);
      }

      // 4. Images that failed to load are invisible in a PDF, not loud.
      if (el.tagName === "IMG" && el.complete && el.naturalWidth === 0) {
        add(n, "missing-asset", describe(el), `did not load: ${el.getAttribute("src")}`);
      }

      const text = ownText(el);
      if (!text) continue;

      // 5. Type too small to read from the back of a room.
      const size = parseFloat(style.fontSize);
      if (size && size < MIN_FONT_PX) {
        add(n, "tiny-type", describe(el), `${size.toFixed(1)}px rendered, floor is ${MIN_FONT_PX}px`);
      }

      // 6. Contrast, WCAG AA: 3:1 for large text, 4.5:1 otherwise.
      const fg = parseRGB(style.color);
      if (fg && fg.a > 0.1) {
        const large = size >= 24 || (size >= 18.66 && parseInt(style.fontWeight, 10) >= 700);
        const need = large ? 3 : 4.5;
        const ratio = contrastRatio(fg, backgroundOf(el));
        if (ratio < need) {
          add(n, "contrast", describe(el), `${ratio.toFixed(2)}:1 against its background, needs ${need}:1`);
        }
      }
    }

    // 7. Two text blocks that visually collide — the defect this guards
    //    against is a diagram where a label lands on another label or on an
    //    arrow. Ancestor/descendant pairs always intersect by construction
    //    and are skipped via contains(). Absolutely/fixed-positioned chrome
    //    (the slide badge, the phase pill) sits over the body box by
    //    design, so the policy is structural rather than a class list:
    //    compare only elements in normal flow (position static or
    //    relative); data-overlap-ignore is the theme-declared escape hatch
    //    for anything else that is meant to sit on top, the same pattern as
    //    data-measure / data-slide. Only leaf text blocks are compared —
    //    ownText, reused from above, is what keeps a container from being
    //    blamed for its children — and a real area of overlap is required
    //    so two inline siblings sharing a line do not fire on a touching
    //    edge.
    //
    //    This is O(n^2) over one slide's text leaves. That is fine at slide
    //    scale, so do not "optimise" it into something that skips a real
    //    collision; MAX_OVERLAP_LEAVES exists so a pathological slide is
    //    skipped outright instead of hanging the check.
    const leaves = [...slide.querySelectorAll("*")].filter((el) => {
      if (el.hasAttribute("data-overlap-ignore")) return false;
      const style = getComputedStyle(el);
      if (!isVisible(el, style)) return false;
      if (style.position === "absolute" || style.position === "fixed") return false;
      return ownText(el) !== "";
    });

    if (leaves.length <= MAX_OVERLAP_LEAVES) {
      for (let i = 0; i < leaves.length; i++) {
        for (let j = i + 1; j < leaves.length; j++) {
          const a = leaves[i];
          const b = leaves[j];
          if (a.contains(b) || b.contains(a)) continue;
          const area = overlapArea(a.getBoundingClientRect(), b.getBoundingClientRect());
          if (area > OVERLAP_MIN_AREA_PX) {
            add(n, "overlap", describe(a), `overlaps ${describe(b)} by ${Math.round(area)}px²`);
          }
        }
      }
    }
  }

  // 8. A self-hosted face that fails to load silently substitutes, which is how
  //    a deck stops looking like the brand without anyone noticing.
  //
  //    document.fonts.check() is the obvious API and the wrong one: it answers
  //    "can this text be rendered", which is true for an unknown family because
  //    the fallback can render it. So inspect the FontFace set instead. A family
  //    with no @font-face at all is a system font the theme chose on purpose and
  //    is skipped — this rule covers the case that actually breaks, a woff2 that
  //    is missing or 404s.
  const faces = [...document.fonts];
  for (const family of FONTS) {
    if (!family) continue;
    const declared = faces.filter(
      (f) => f.family.replace(/["']/g, "").toLowerCase() === family.toLowerCase());
    if (declared.length === 0) continue;
    if (!declared.some((f) => f.status === "loaded")) {
      const statuses = [...new Set(declared.map((f) => f.status))].join(", ");
      add(0, "font-fallback", family,
        `declared via @font-face but no face loaded (${statuses}); the browser substituted another`);
    }
  }

  return findings;
})()
