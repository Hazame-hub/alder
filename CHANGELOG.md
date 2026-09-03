# Changelog

## [0.4.0](https://github.com/Hazame-hub/alder/compare/v0.3.0...v0.4.0) (2026-09-03)


### Features

* stage several changes and apply them as one reviewed set ([#16](https://github.com/Hazame-hub/alder/issues/16)) ([1f4d30f](https://github.com/Hazame-hub/alder/commit/1f4d30f037b8c812b7b0f04917c50253d3421a45))

## [0.3.0](https://github.com/Hazame-hub/alder/compare/v0.2.0...v0.3.0) (2026-09-03)


### Features

* set passwords, pick DNs, and copy entries ([#14](https://github.com/Hazame-hub/alder/issues/14)) ([24defdc](https://github.com/Hazame-hub/alder/commit/24defdc1f044f9ae21de15511741daff61ce2d37))

## [0.2.0](https://github.com/Hazame-hub/alder/compare/v0.1.2...v0.2.0) (2026-09-03)


### Features

* **web:** remember everything about a connection except the password ([#12](https://github.com/Hazame-hub/alder/issues/12)) ([a6add69](https://github.com/Hazame-hub/alder/commit/a6add6998ea2c115219ebea1b6936fed4471efce))

## [0.1.2](https://github.com/Hazame-hub/alder/compare/v0.1.1...v0.1.2) (2026-09-03)


### Fixes

* **ci:** copy the binary from the platform path dockers_v2 stages it at ([#10](https://github.com/Hazame-hub/alder/issues/10)) ([e6629dc](https://github.com/Hazame-hub/alder/commit/e6629dcba27be433a1b00ee9acacf4eea998e6f2))
* **ci:** install syft so the SBOMs GoReleaser is asked for can be built ([#8](https://github.com/Hazame-hub/alder/issues/8)) ([f050fd0](https://github.com/Hazame-hub/alder/commit/f050fd0db31f68fbc23489c3aa597737faf2ecac))
* **ci:** stop the CLA lock breaking releases, and make a half-release recoverable ([#7](https://github.com/Hazame-hub/alder/issues/7)) ([25c923c](https://github.com/Hazame-hub/alder/commit/25c923c2b920e6ce5dec94f94a305ac353be57ee))

## [0.1.1](https://github.com/Hazame-hub/alder/compare/v0.1.0...v0.1.1) (2026-09-03)


### Fixes

* **build:** use npm ci in the web task so the tree stays clean ([#5](https://github.com/Hazame-hub/alder/issues/5)) ([4b3cb6e](https://github.com/Hazame-hub/alder/commit/4b3cb6e043236b1166e0c841a9c7763b0ab38733))

## 0.1.0 (2026-09-03)


### Features

* AGPL-3.0, with the section 13 source offer served by the product ([85fec71](https://github.com/Hazame-hub/alder/commit/85fec716984363907e733fad85e9c23b43ef12d7))
* **api:** spec-first HTTP API, session store and the alder serve command ([68c4a82](https://github.com/Hazame-hub/alder/commit/68c4a827464985c55fc1afd3466b96cbc13cb314))
* **directory:** driver interface, LDAP driver and conformance suite ([5987281](https://github.com/Hazame-hub/alder/commit/59872814de22b98dc7c0b253d67a90d66f72aa86))
* **ldif:** RFC 2849 reader and writer ([c4a78d1](https://github.com/Hazame-hub/alder/commit/c4a78d185bf5887732b5a7272958d147ef222a45))
* M0 foundations ([61fb44e](https://github.com/Hazame-hub/alder/commit/61fb44e6ac09488b3e8606ed7426880c61219d99))
* **schema:** RFC 4512 parser, index and presentation kinds ([55b9a2f](https://github.com/Hazame-hub/alder/commit/55b9a2f89c2100bc75aae185289f30a654a5ab53))
* **web:** the single-page application, embedded in the binary ([78663ca](https://github.com/Hazame-hub/alder/commit/78663cafa0c58b0b314d0c647a235c12d8b82245))


### Fixes

* keep the embed directory in git so a fresh clone builds without Node ([7449c30](https://github.com/Hazame-hub/alder/commit/7449c30d12db330902057ff88c853c887cbf575b))
* **web:** restore the embed placeholder from the Vite build, not the Makefile ([074a6ad](https://github.com/Hazame-hub/alder/commit/074a6ad1854937f4b93ea6b1880187ce29d0cb65))
* **web:** stop a background refetch discarding an in-progress edit ([9d00657](https://github.com/Hazame-hub/alder/commit/9d00657edde1c0974408efa1c8d02d5b132dd4f1))


### Documentation

* add a contributor licence agreement, enforced on every pull request ([947f2c4](https://github.com/Hazame-hub/alder/commit/947f2c4cf4fd4b94dbd0c637103dd3121b1a30e9))
* move the decisions log into docs/ ([7a00490](https://github.com/Hazame-hub/alder/commit/7a00490ad56fbb199ea5f57706fd7a2414c39ab1))
* README, Dockerfile web stage, and the decisions log for M1-M4 ([f43ce97](https://github.com/Hazame-hub/alder/commit/f43ce97bedb6d92114f173411740c51fb3e9097c))
* security policy, contributing guide and release plumbing ([60ce142](https://github.com/Hazame-hub/alder/commit/60ce142f80ab9ac36102662c7ea4cc69e1d7872e))
