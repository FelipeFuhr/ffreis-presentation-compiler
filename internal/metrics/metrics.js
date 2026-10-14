// Per-slide layout metrics, for model-assisted review (`make review`).
//
// This is a sibling of probe.js, not an extension of it: probe.js is
// refactored into one function per check rule and is at its complexity
// ceiling, and this is a different job entirely — reporting numbers for a
// model (or a human) to read, not pass/fail findings. ffreis-modelctl is
// text-only, which is exactly why `make review` hands it these metrics
// rather than the rendered PNG files: a vision-model lane over the images is a
// different, out-of-scope lane.
//
// Returns [{slide, layout, boxes, textLength, bulletCount, fontSizesPx,
// palette, density, imageCount}].
(() => {
  // Explicit comparator rather than a bare .sort(). These are rgb() strings,
  // so the default lexicographic order is already correct — but the default
  // coerces to string, which IS wrong for numeric input, and a reader (or a
  // scanner) cannot tell the two cases apart from the call site.
  const byCodepoint = (a, b) => {
    if (a < b) return -1;
    if (a > b) return 1;
    return 0;
  };

  const isVisible = (el, style) =>
    style.display !== "none" &&
    style.visibility !== "hidden" &&
    Number.parseFloat(style.opacity || "1") > 0.05 &&
    el.getBoundingClientRect().width > 0 &&
    el.getBoundingClientRect().height > 0;

  // Direct text content only, so a wrapper is never counted on top of its
  // own children — the same leaf definition internal/check/probe.js uses for
  // tiny-type, contrast and overlap.
  const ownText = (el) =>
    [...el.childNodes]
      .filter((n) => n.nodeType === Node.TEXT_NODE)
      .map((n) => n.textContent.trim())
      .join(" ")
      .trim();

  const textLeaves = (slide) =>
    [...slide.querySelectorAll("*")].filter((el) => {
      const style = getComputedStyle(el);
      return isVisible(el, style) && ownText(el) !== "";
    });

  const describe = (el) => {
    const cls = (el.className && typeof el.className === "string")
      ? "." + el.className.trim().split(/\s+/).slice(0, 2).join(".")
      : "";
    return `${el.tagName.toLowerCase()}${cls}`;
  };

  // Box geometry: the slide's own direct children. That is every theme's
  // chrome structure (kicker, title, body, footer…) for a framed slide, or
  // whatever a bare layout (cover, section) emits at its top level — neither
  // case needs a single theme-specific class name hardcoded here.
  //
  // Coordinates are relative to the slide's own top-left corner, not the
  // page's: on screen every slide is one more section stacked down a single
  // scrollable page (see base.gohtml's @media screen rule), so an
  // un-adjusted getBoundingClientRect() would grow monotonically from slide
  // to slide and make "where does the kicker usually sit" impossible to
  // answer by comparing slides.
  const boxes = (slide) => {
    const frame = slide.getBoundingClientRect();
    return [...slide.children].map((el) => {
      const r = el.getBoundingClientRect();
      return {
        element: describe(el),
        x: Math.round(r.left - frame.left), y: Math.round(r.top - frame.top),
        width: Math.round(r.width), height: Math.round(r.height),
      };
    });
  };

  const fontSizesPx = (leaves) =>
    [...new Set(leaves.map((el) => Number.parseFloat(getComputedStyle(el).fontSize)))]
      .filter((n) => !Number.isNaN(n))
      .sort((a, b) => a - b);

  // Palette: the distinct text and background colours actually painted on
  // this slide's text leaves, so a reviewer can see at a glance whether a
  // slide strayed from the deck's shared tokens.
  const palette = (leaves) => {
    const transparent = "rgba(0, 0, 0, 0)";
    const colours = new Set();
    for (const el of leaves) {
      const style = getComputedStyle(el);
      colours.add(style.color);
      if (style.backgroundColor && style.backgroundColor !== transparent) {
        colours.add(style.backgroundColor);
      }
    }
    return [...colours].sort(byCodepoint);
  };

  // Density: text area over slide area. Leaves can share a touching edge, so
  // this is an approximation of coverage, not an exact one — good enough for
  // an advisory "does this slide look crowded or sparse" signal.
  const density = (slide, leaves) => {
    const slideArea = slide.clientWidth * slide.clientHeight;
    if (slideArea <= 0) return 0;
    const textArea = leaves.reduce((sum, el) => {
      const r = el.getBoundingClientRect();
      return sum + r.width * r.height;
    }, 0);
    return Math.round((textArea / slideArea) * 10000) / 10000;
  };

  const imageCount = (slide) =>
    [...slide.querySelectorAll("img")].filter((el) => isVisible(el, getComputedStyle(el))).length;

  const textLength = (slide) => (slide.textContent || "").replace(/\s+/g, " ").trim().length;

  const slideMetrics = (slide, n) => {
    const leaves = textLeaves(slide);
    return {
      slide: n,
      layout: slide.dataset.layout || "",
      boxes: boxes(slide),
      textLength: textLength(slide),
      bulletCount: slide.querySelectorAll("li").length,
      fontSizesPx: fontSizesPx(leaves),
      palette: palette(leaves),
      density: density(slide, leaves),
      imageCount: imageCount(slide),
    };
  };

  const results = [];
  for (const slide of document.querySelectorAll("[data-slide]")) {
    const n = Number(slide.dataset.slide) || 0;
    results.push(slideMetrics(slide, n));
  }
  return results;
})()
