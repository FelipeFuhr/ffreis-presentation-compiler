# Changelog

## 1.0.0 (2026-10-23)


### Features

* **check:** add overlap and pdf-pages rules ([#9](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/9)) ([db576ca](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/db576cae84e6c9201ab4dff94e9815bf434bbac5))
* **ci:** add incremental in-diff mutation gate on pull_request ([#19](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/19)) ([9bc51c3](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/9bc51c3274f50abf435c72d490513e4c6178e399))
* compile declarative decks to HTML and PDF ([#1](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/1)) ([3f2f8c0](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/3f2f8c01624f399fd1532fe9807c6572c7659658))
* **contract:** reject an alt that says nothing, declare the quality flags ([#17](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/17)) ([018b766](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/018b766370f8dd95ad1103571592185385f3862d))
* **contract:** validate a deck's field vocabulary before rendering ([#8](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/8)) ([b1cb38f](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/b1cb38fce10da5b52eead933d5e884133b0aff7f))
* D2 diagrams, CSV-sourced tables, OCR content parity ([#16](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/16)) ([2bbebfe](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/2bbebfe46e3f4e51c48838025bdec07b7d258e5b))
* **golden:** visual regression, model-assisted review, and a row-arity rule ([#12](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/12)) ([008c5ec](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/008c5ecfda9bdd9bc31b95527d70679371ee50e8))
* PNG and PPTX export ([#2](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/2)) ([57ab983](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/57ab9835fbed7c1cb2883969743d58e3fc1572cb))
* **pptx:** native editable export, and measure the exported file ([#25](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/25)) ([301113a](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/301113a068f8019b6c206968906bd77c71861fd7))
* **pptx:** Write verifies its own output before shipping it ([#24](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/24)) ([168563b](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/168563bc932c88126c2af96215baf400e77b5f3d))
* **seo:** emit a deck's discovery surface, and move the shell to a rem basis ([#10](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/10)) ([6328fcb](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/6328fcb3b880079c1f3d2b5166b556c847abc151))
* **serve:** add a live-reloading preview server ([#11](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/11)) ([e6a948a](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/e6a948a46067a60151c9e1f4c11bbf68614f188f))
* **theme:** add a seq template function for fixed grids ([#13](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/13)) ([c6c39e5](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/c6c39e5cfdeb56b0a4f467f6bb057980b3d63c8d))


### Bug Fixes

* **chrome:** retry a failed browser launch once ([#14](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/14)) ([7cfb431](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/7cfb4311e88c6c5d78ae512ffed006e0b7311f3a))
* **ci:** bump stale workflow pins, target a public runner, clear stdlib CVEs ([#3](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/3)) ([9bc1879](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/9bc1879caa76b788c258308deef15a2801279465))
* **ci:** bump the SBOM pin off a vanished upstream action SHA ([#5](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/5)) ([a38234e](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/a38234eff5ecd44605eacba61393ef848d95ac59))
* **ci:** re-trigger the draft-gated gates when a PR is promoted ([#7](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/7)) ([14468e1](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/14468e1fc098f962d58704086962e54388071d00))
* **contract:** a chart block satisfies a required media field ([#15](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/15)) ([553ee40](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/553ee405e98168dc8b285c974d0c7e9b19171545))
* **fontsource:** name the variant a web font cannot supply ([6fcab4a](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/6fcab4a12ec167ffd4c866b7c980e6ec14eadc67))
* **io:** percent-encode file:// URLs, write pptx atomically, reject symlinked dirs ([#6](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/6)) ([d1badb7](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/d1badb7a8351b3299289aae4270729d3601c2fa0))
* **pptx:** embed the typeface as EOT, not headerless sfnt ([123afe6](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/123afe67d291310add104f92ced9468ed3260fcb))
* **pptx:** every exported file carried an invalid MS-DOS timestamp ([#20](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/20)) ([1cc6755](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/1cc6755afb0e7ce34360015fa4f727037c77a904))
* **pptx:** every exported file declared zip version 0.0 ([#21](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/21)) ([806a6f4](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/806a6f443a2bc5506f4801c69a28329ccf425db0))
* **pptx:** the notes master needs its own theme part ([#23](https://github.com/FelipeFuhr/ffreis-presentation-compiler/issues/23)) ([ff11ce0](https://github.com/FelipeFuhr/ffreis-presentation-compiler/commit/ff11ce09ffcc691aab7c25306623bdebd029ef00))
