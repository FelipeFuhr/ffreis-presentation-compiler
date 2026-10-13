---
layout: split
kicker: Trend
title: The same figures, generated at build time
chart:
  generator: python
  spec: charts/bars.py
  data: data/throughput.csv
  args: ["--title", "Units per week by quarter"]
alt: Bar chart of units per week rising from 9,800 to 12,400 across five quarters
---
This chart is **not** an image checked into the repo. It is produced at build
time from `data/throughput.csv`, and the compiler hands the generator the deck's
own design tokens — so the bars are the theme's accent colour by construction,
not by someone remembering to match it.

Change a number in the CSV and the chart changes. Change the theme and the
chart follows.
