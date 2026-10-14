// Deterministic slide checks, evaluated in the page after render.
//
// Everything here is measured, not guessed: the browser has already done
// layout, so "does this text fit" is a number rather than an estimate. That is
// the whole reason the compiler renders HTML instead of placing boxes.
//
// Returns [{slide, rule, element, detail}]. Severity is decided in Go.
//
// Each rule below is its own top-level function, deliberately not nested
// inside the slide loop: nesting is exactly what cognitive-complexity
// tooling penalizes, and a probe that grew to eight rules in one shared
// closure (javascript:S3776) is exactly that shape. The outer IIFE only
// orchestrates: set up the shared primitives, iterate slides, call each
// rule, concatenate.
(() => {
  const TOL = __TOL_PX__;     // px; see Options.TolerancePx in check.go
  const MIN_FONT_PX = __MIN_FONT_PX__;
  const OVERLAP_MIN_AREA_PX = __OVERLAP_MIN_AREA_PX__; // px^2; see Options.OverlapMinAreaPx
  const FONTS = __FONTS__;
  // A slide with more leaf text blocks than this is pathological (almost
  // certainly a generated diagram with one span per glyph, not authored
  // content); checkOverlap skips such a slide rather than running an O(n^2)
  // pairwise comparison that would hang the check instead of reporting on it.
  const MAX_OVERLAP_LEAVES = 400;

  // The one place a finding object is built, so every rule below shares the
  // same shape without repeating the field names once per rule.
  const finding = (slide, rule, element, detail) => ({ slide, rule, element, detail });

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
    const parts = m[1].split(",").map((p) => Number.parseFloat(p));
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
    Number.parseFloat(style.opacity || "1") > 0.05 &&
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

  // 1. The slide frame itself must not have more content than it can show.
  const checkSlideOverflow = (slide, n) => {
    const out = [];
    if (slide.scrollHeight > slide.clientHeight + TOL) {
      out.push(finding(n, "overflow", "slide",
        `content is ${Math.round(slide.scrollHeight - slide.clientHeight)}px taller than the slide`));
    }
    if (slide.scrollWidth > slide.clientWidth + TOL) {
      out.push(finding(n, "overflow", "slide",
        `content is ${Math.round(slide.scrollWidth - slide.clientWidth)}px wider than the slide`));
    }
    return out;
  };

  // 2. Any box the theme marked as measurable — the clipping boxes.
  const checkMeasuredBoxes = (slide, n) => {
    const out = [];
    for (const box of slide.querySelectorAll("[data-measure]")) {
      if (box.scrollHeight > box.clientHeight + TOL) {
        out.push(finding(n, "overflow", describe(box),
          `clipped: ${Math.round(box.scrollHeight - box.clientHeight)}px of content does not fit`));
      }
    }
    return out;
  };

  // Which edges of frame rect escapes, if any — split out of checkBounds so
  // the four-way edge comparison has its own, trivially-low complexity
  // rather than adding to the loop that calls it.
  const escapedEdges = (rect, frame) =>
    [
      rect.left < frame.left - TOL ? "left" : null,
      rect.right > frame.right + TOL ? "right" : null,
      rect.top < frame.top - TOL ? "top" : null,
      rect.bottom > frame.bottom + TOL ? "bottom" : null,
    ].filter(Boolean);

  // 3. Nothing may escape the slide, since the slide is the printed page.
  const checkBounds = (slide, n) => {
    const frame = slide.getBoundingClientRect();
    const out = [];
    for (const el of slide.querySelectorAll("*")) {
      const style = getComputedStyle(el);
      if (!isVisible(el, style)) continue;
      const over = escapedEdges(el.getBoundingClientRect(), frame);
      if (over.length) {
        out.push(finding(n, "out-of-bounds", describe(el), `escapes the slide on the ${over.join(", ")}`));
      }
    }
    return out;
  };

  // 4. Images that failed to load are invisible in a PDF, not loud. Scoped
  // to the same visibility gate as every other element-level rule, so a
  // deliberately hidden broken image (as opposed to an authored one) does
  // not fire.
  const checkAssets = (slide, n) => {
    const out = [];
    for (const el of slide.querySelectorAll("img")) {
      if (!isVisible(el, getComputedStyle(el))) continue;
      if (el.complete && el.naturalWidth === 0) {
        out.push(finding(n, "missing-asset", describe(el), `did not load: ${el.getAttribute("src")}`));
      }
    }
    return out;
  };

  // 5. Type too small to read from the back of a room.
  const checkTinyType = (slide, n) => {
    const out = [];
    for (const el of slide.querySelectorAll("*")) {
      const style = getComputedStyle(el);
      if (!isVisible(el, style) || !ownText(el)) continue;
      const size = Number.parseFloat(style.fontSize);
      if (size && size < MIN_FONT_PX) {
        out.push(finding(n, "tiny-type", describe(el), `${size.toFixed(1)}px rendered, floor is ${MIN_FONT_PX}px`));
      }
    }
    return out;
  };

  // 6. Contrast, WCAG AA: 3:1 for large text, 4.5:1 otherwise.
  const checkContrast = (slide, n) => {
    const out = [];
    for (const el of slide.querySelectorAll("*")) {
      const style = getComputedStyle(el);
      if (!isVisible(el, style) || !ownText(el)) continue;
      const fg = parseRGB(style.color);
      if (!fg || fg.a <= 0.1) continue;
      const size = Number.parseFloat(style.fontSize);
      const large = size >= 24 || (size >= 18.66 && Number.parseInt(style.fontWeight, 10) >= 700);
      const need = large ? 3 : 4.5;
      const ratio = contrastRatio(fg, backgroundOf(el));
      if (ratio < need) {
        out.push(finding(n, "contrast", describe(el), `${ratio.toFixed(2)}:1 against its background, needs ${need}:1`));
      }
    }
    return out;
  };

  // The leaves checkOverlap compares: elements whose own text is non-empty
  // (a container is never blamed for its children), that are visible, in
  // normal CSS flow (position static or relative — absolutely/fixed
  // positioned chrome like the slide badge or phase pill sits over the body
  // box by design and is excluded structurally, not via a class list), and
  // not opted out via the theme-declared data-overlap-ignore escape hatch
  // (the same pattern as data-measure / data-slide).
  const overlapLeaves = (slide) =>
    [...slide.querySelectorAll("*")].filter((el) => {
      if (el.dataset.overlapIgnore !== undefined) return false;
      const style = getComputedStyle(el);
      if (!isVisible(el, style)) return false;
      if (style.position === "absolute" || style.position === "fixed") return false;
      return ownText(el) !== "";
    });

  // 7. Two text blocks that visually collide — the defect this guards
  //    against is a diagram where a label lands on another label or on an
  //    arrow. Ancestor/descendant pairs always intersect by construction
  //    and are skipped via contains(); a real area of overlap is required
  //    so two inline siblings sharing a line do not fire on a touching edge.
  //
  //    This is O(n^2) over one slide's text leaves. That is fine at slide
  //    scale, so do not "optimise" it into something that skips a real
  //    collision; MAX_OVERLAP_LEAVES exists so a pathological slide is
  //    skipped outright instead of hanging the check.
  const checkOverlap = (slide, n) => {
    const leaves = overlapLeaves(slide);
    const out = [];
    if (leaves.length > MAX_OVERLAP_LEAVES) return out;
    for (let i = 0; i < leaves.length; i++) {
      for (let j = i + 1; j < leaves.length; j++) {
        const a = leaves[i];
        const b = leaves[j];
        if (a.contains(b) || b.contains(a)) continue;
        const area = overlapArea(a.getBoundingClientRect(), b.getBoundingClientRect());
        if (area > OVERLAP_MIN_AREA_PX) {
          out.push(finding(n, "overlap", describe(a), `overlaps ${describe(b)} by ${Math.round(area)}px²`));
        }
      }
    }
    return out;
  };

  // 8. A self-hosted face that fails to load silently substitutes, which is
  //    how a deck stops looking like the brand without anyone noticing.
  //    Deck-level, not per-slide, so findings use slide 0.
  //
  //    document.fonts.check() is the obvious API and the wrong one: it
  //    answers "can this text be rendered", which is true for an unknown
  //    family because the fallback can render it. So inspect the FontFace
  //    set instead. A family with no @font-face at all is a system font the
  //    theme chose on purpose and is skipped — this rule covers the case
  //    that actually breaks, a woff2 that is missing or 404s.
  const checkFonts = () => {
    const out = [];
    const faces = [...document.fonts];
    for (const family of FONTS) {
      if (!family) continue;
      const declared = faces.filter(
        (f) => f.family.replace(/["']/g, "").toLowerCase() === family.toLowerCase());
      if (declared.length === 0) continue;
      if (!declared.some((f) => f.status === "loaded")) {
        const statuses = [...new Set(declared.map((f) => f.status))].join(", ");
        out.push(finding(0, "font-fallback", family,
          `declared via @font-face but no face loaded (${statuses}); the browser substituted another`));
      }
    }
    return out;
  };

  const perSlideRules = [
    checkSlideOverflow, checkMeasuredBoxes, checkBounds,
    checkAssets, checkTinyType, checkContrast, checkOverlap,
  ];

  const findings = [];
  for (const slide of document.querySelectorAll("[data-slide]")) {
    const n = Number(slide.dataset.slide) || 0;
    for (const rule of perSlideRules) {
      findings.push(...rule(slide, n));
    }
  }
  findings.push(...checkFonts());
  return findings;
})()
