// Measure the elements a theme's PPTX layouts name.
//
// A native .pptx cannot take its geometry from CSS the way the HTML export
// does, so the first version of these layouts stated every rectangle by hand.
// That was wrong twice over: absolute positions leave a hole wherever an
// optional shape is skipped, and a table's rows were divided evenly instead of
// sized by content, which put the tables 14-18% off the reference render.
//
// So the browser that already laid the slide out is asked where things are.
// The content still comes from the deck model -- a table stays a real a:tbl
// and text stays editable -- but the numbers come from the engine that has the
// fonts. That is the one authority on where a line of Roboto actually ends.
//
// Returns [{slide, frameW, frameH, elements: {selector: measurement}}].
// Pixels throughout; the Go side converts using frameW against the deck's own
// width, so the result is correct whatever the render scale.
(() => {
  const SELECTORS = __SELECTORS__;

  const num = (v) => {
    const n = Number.parseFloat(v);
    return Number.isFinite(n) ? n : 0;
  };

  // The run-level formatting a PPTX text run can carry. Anything CSS can do
  // that OOXML cannot is simply not read, rather than approximated.
  const styleOf = (el) => {
    const s = getComputedStyle(el);
    const size = num(s.fontSize);
    return {
      fontSizePx: size,
      // `normal` has no numeric value; 1.2 is the CSS initial used by every
      // engine, and a wrong line height here only changes a text box's
      // internal leading, never where the box sits.
      lineHeightPx: s.lineHeight === "normal" ? size * 1.2 : num(s.lineHeight),
      weight: Number.parseInt(s.fontWeight, 10) || 400,
      italic: s.fontStyle === "italic",
      color: s.color,
      align: s.textAlign,
      letterSpacingPx: s.letterSpacing === "normal" ? 0 : num(s.letterSpacing),
      family: s.fontFamily,
      transform: s.textTransform,
    };
  };

  // A table's real row heights and column widths.
  //
  // `rows` spans thead and tbody in document order, which is the order the
  // a:tbl is written in, so the two line up without the Go side knowing
  // anything about table structure.
  const gridOf = (el) => {
    if (el.tagName !== "TABLE" || el.rows.length === 0) return null;
    const rows = [...el.rows].map((tr) => tr.getBoundingClientRect().height);
    const cols = [...el.rows[0].cells].map((c) => c.getBoundingClientRect().width);
    return { rows, cols };
  };

  const out = [];
  for (const slide of document.querySelectorAll("[data-slide]")) {
    const frame = slide.getBoundingClientRect();
    const elements = {};

    for (const selector of SELECTORS) {
      const el = slide.querySelector(selector);
      if (!el) continue;
      const r = el.getBoundingClientRect();
      // A zero-area element is present but not painted -- an empty optional
      // region. Reporting it would place an invisible shape; omitting it lets
      // the mapper treat the region as absent, which it is.
      if (r.width <= 0 || r.height <= 0) continue;

      elements[selector] = Object.assign(
        {
          x: r.left - frame.left,
          y: r.top - frame.top,
          w: r.width,
          h: r.height,
        },
        styleOf(el),
        { grid: gridOf(el) },
      );
    }

    out.push({
      slide: Number(slide.dataset.slide) || 0,
      frameW: frame.width,
      frameH: frame.height,
      elements,
    });
  }
  return out;
})()
