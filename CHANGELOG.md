# Changelog

## [1.4.0](https://github.com/Hazame-hub/alder/compare/v1.3.2...v1.4.0) (2026-09-12)


### Features

* target allowlist, resource ceiling, and a first-class plan ([#94](https://github.com/Hazame-hub/alder/issues/94)) ([37c8fba](https://github.com/Hazame-hub/alder/commit/37c8fba4380fddb710c88113fe7f78af33f0a9cb))


### Performance

* **api:** measure the paths that were never measured, fix two ([#93](https://github.com/Hazame-hub/alder/issues/93)) ([294dec1](https://github.com/Hazame-hub/alder/commit/294dec1f94715d951c73bcfde828d12d888e7973))

## [1.3.2](https://github.com/Hazame-hub/alder/compare/v1.3.1...v1.3.2) (2026-09-12)


### Performance

* **export:** stop holding the tree exports twice ([#91](https://github.com/Hazame-hub/alder/issues/91)) ([82fac49](https://github.com/Hazame-hub/alder/commit/82fac497685afe9c91908fe9be1ee5a31dd857b3))

## [1.3.1](https://github.com/Hazame-hub/alder/compare/v1.3.0...v1.3.1) (2026-09-12)


### Performance

* **api:** stream the search response instead of building it ([#88](https://github.com/Hazame-hub/alder/issues/88)) ([148749f](https://github.com/Hazame-hub/alder/commit/148749f822e0a3b3f7630557336dc2de6fdda171))

## [1.3.0](https://github.com/Hazame-hub/alder/compare/v1.2.0...v1.3.0) (2026-09-12)


### Features

* **entry:** offer the operational attributes a server lets you set ([#83](https://github.com/Hazame-hub/alder/issues/83)) ([fa33ce4](https://github.com/Hazame-hub/alder/commit/fa33ce44b62f01176726fab2d0f164e558cb7621))
* **export:** draw a subtree as the tree it is ([#85](https://github.com/Hazame-hub/alder/issues/85)) ([62a16bc](https://github.com/Hazame-hub/alder/commit/62a16bc4d27d92d6ea8f31213c2441dab610751d))
* **export:** render a subtree as nested YAML ([#86](https://github.com/Hazame-hub/alder/issues/86)) ([713aa76](https://github.com/Hazame-hub/alder/commit/713aa7611f9f94c36e72ab3b9a9e9df802016941))

## [1.2.0](https://github.com/Hazame-hub/alder/compare/v1.1.0...v1.2.0) (2026-09-12)


### Performance

* **export:** stream the LDIF export instead of building it ([#80](https://github.com/Hazame-hub/alder/issues/80)) ([eae70ff](https://github.com/Hazame-hub/alder/commit/eae70fff996c6a2273f7fa2b4eda85e8324886d5))
* **schema:** work out an entry's requirements once per class set ([#82](https://github.com/Hazame-hub/alder/issues/82)) ([57e8f0d](https://github.com/Hazame-hub/alder/commit/57e8f0db55779003f2ce29ce8aec0d16675e6016))

## [1.1.0](https://github.com/Hazame-hub/alder/compare/v1.0.1...v1.1.0) (2026-09-10)


### Features

* **tree:** count the children a session may not see, and stop overclaiming ([#77](https://github.com/Hazame-hub/alder/issues/77)) ([4654f62](https://github.com/Hazame-hub/alder/commit/4654f62c3a512168633190494c2aef7652d588c9))


### Performance

* **inventory:** tally a hundred thousand entries instead of ten thousand ([#79](https://github.com/Hazame-hub/alder/issues/79)) ([20fff2c](https://github.com/Hazame-hub/alder/commit/20fff2c35e2a1490b84f2c51e1727177149e3037))

## [1.0.1](https://github.com/Hazame-hub/alder/compare/v1.0.0...v1.0.1) (2026-09-10)


### Fixes

* **compare:** stop reporting a denied attribute as a difference ([#74](https://github.com/Hazame-hub/alder/issues/74)) ([83896d7](https://github.com/Hazame-hub/alder/commit/83896d750a8547d9bc023494dbb821049b02b5d0))
* **members:** stop reporting an unreadable member as a deleted one ([#76](https://github.com/Hazame-hub/alder/issues/76)) ([2092f3d](https://github.com/Hazame-hub/alder/commit/2092f3db97e9f61fd25ae99018ce6bed7cf1132e))

## [1.0.0](https://github.com/Hazame-hub/alder/compare/v0.12.2...v1.0.0) (2026-09-09)


### ⚠ BREAKING CHANGES

* AttributeComparison.comparable is removed and the status enum gains "withheld". A client that switched on status and treated an unknown value as "same" now sees "withheld" for a sensitive attribute held by both entries -- which is the point: it never meant "same".

### Features

* state what 1.0 promises, and fix the compare shape before freezing it ([#71](https://github.com/Hazame-hub/alder/issues/71)) ([9a32a75](https://github.com/Hazame-hub/alder/commit/9a32a757dac1222f8ac2d6c350b684513a8f7ab1))

## [0.12.2](https://github.com/Hazame-hub/alder/compare/v0.12.1...v0.12.2) (2026-09-09)


### Fixes

* **ansible:** keep the bind password out of the rename command line ([#69](https://github.com/Hazame-hub/alder/issues/69)) ([4c065cb](https://github.com/Hazame-hub/alder/commit/4c065cb9341d6a6e389a1f618889a61825099c7b))

## [0.12.1](https://github.com/Hazame-hub/alder/compare/v0.12.0...v0.12.1) (2026-09-09)


### Fixes

* **ansible:** a delete naming no values emptied nothing ([#67](https://github.com/Hazame-hub/alder/issues/67)) ([df93bc4](https://github.com/Hazame-hub/alder/commit/df93bc4a439822ae3d7ced235342a781c67f726c))

## [0.12.0](https://github.com/Hazame-hub/alder/compare/v0.11.0...v0.12.0) (2026-09-09)


### Features

* compare two entries, attribute by attribute ([#62](https://github.com/Hazame-hub/alder/issues/62)) ([2494fc3](https://github.com/Hazame-hub/alder/commit/2494fc39a41ecd3c2874ffbfd3b24cd860655a91))
* jump to a DN, a filter, or a name from one box ([#64](https://github.com/Hazame-hub/alder/issues/64)) ([aef624d](https://github.com/Hazame-hub/alder/commit/aef624dc8b00c0575d7031792ea59dc8d4792fa9))
* resolve a group's membership through nested groups ([#60](https://github.com/Hazame-hub/alder/issues/60)) ([80b0d50](https://github.com/Hazame-hub/alder/commit/80b0d50fff51758f83cea78939e6f9f1b729219a))
* tally what values an attribute holds across a subtree ([#63](https://github.com/Hazame-hub/alder/issues/63)) ([36f3d25](https://github.com/Hazame-hub/alder/commit/36f3d25f025d3220764656187650dd6a9fc660bf))


### Documentation

* describe the four features 0.12.0 adds ([#65](https://github.com/Hazame-hub/alder/issues/65)) ([8aee51d](https://github.com/Hazame-hub/alder/commit/8aee51dc3ee26d0ff7f4f2042758283402299ca2))

## [0.11.0](https://github.com/Hazame-hub/alder/compare/v0.10.0...v0.11.0) (2026-09-07)


### Features

* import can update entries that already exist ([#57](https://github.com/Hazame-hub/alder/issues/57)) ([cdc9540](https://github.com/Hazame-hub/alder/commit/cdc9540711257b509df296521eb360dc4cff2d87))


### Documentation

* describe importing over entries that already exist ([#59](https://github.com/Hazame-hub/alder/issues/59)) ([c94d3bc](https://github.com/Hazame-hub/alder/commit/c94d3bce6ec3eef0bde8f99a1bc5fcdf02f33e1a))

## [0.10.0](https://github.com/Hazame-hub/alder/compare/v0.9.0...v0.10.0) (2026-09-06)


### Features

* offer the playbook format from the search and object tables ([#54](https://github.com/Hazame-hub/alder/issues/54)) ([26c4b0b](https://github.com/Hazame-hub/alder/commit/26c4b0b4c8cc0d757a4571b9baedc7c002b4d282))

## [0.9.0](https://github.com/Hazame-hub/alder/compare/v0.8.0...v0.9.0) (2026-09-06)


### Features

* export a subtree as a playbook that enforces it, not one that hopes ([#52](https://github.com/Hazame-hub/alder/issues/52)) ([24e8be7](https://github.com/Hazame-hub/alder/commit/24e8be747131f95d44f2652c95172e2182313733))
* hand back the ldapsearch that runs the search you just ran ([#49](https://github.com/Hazame-hub/alder/issues/49)) ([832348b](https://github.com/Hazame-hub/alder/commit/832348bb6abab17fbcb686e0d1969c5151bceef6))
* raise the changeset cap to 2000, measured rather than guessed ([#48](https://github.com/Hazame-hub/alder/issues/48)) ([a34f197](https://github.com/Hazame-hub/alder/commit/a34f19739f757e0e571c09c6197726a575f8ad10))


### Documentation

* describe the ldapsearch command and the enforcing Ansible export ([#53](https://github.com/Hazame-hub/alder/issues/53)) ([4dc18e0](https://github.com/Hazame-hub/alder/commit/4dc18e0255c28d831417d002e45710e4b7031b34))
* say that staging many changes for one review is in scope ([#47](https://github.com/Hazame-hub/alder/issues/47)) ([811f3ae](https://github.com/Hazame-hub/alder/commit/811f3ae461749b092aba36138840f466ee94fd01))

## [0.8.0](https://github.com/Hazame-hub/alder/compare/v0.7.0...v0.8.0) (2026-09-06)


### Features

* choose a table's columns, and export what it is showing ([#44](https://github.com/Hazame-hub/alder/issues/44)) ([d233191](https://github.com/Hazame-hub/alder/commit/d233191c3c5136063cd88bd920b19508b284b3c7))
* delete a container by staging what is under it ([#42](https://github.com/Hazame-hub/alder/issues/42)) ([f84c3bb](https://github.com/Hazame-hub/alder/commit/f84c3bb2210e8aa708e51b8d0cbe14115f43bd28))
* find every entry that names this one ([#40](https://github.com/Hazame-hub/alder/issues/40)) ([72d4d43](https://github.com/Hazame-hub/alder/commit/72d4d43a889e73b6940d2dc41ec4a5384022c2c7))
* say which attribute names an entry, and remove one reference ([#45](https://github.com/Hazame-hub/alder/issues/45)) ([2e82513](https://github.com/Hazame-hub/alder/commit/2e825137439882f151f3389838e68bc89e58054c))
* stage a parsed LDIF document into the changeset ([#41](https://github.com/Hazame-hub/alder/issues/41)) ([111447d](https://github.com/Hazame-hub/alder/commit/111447d45bef9c61f8b66e169871d28ca254b9d5))


### Fixes

* enforce the changeset bound the spec has always declared ([#38](https://github.com/Hazame-hub/alder/issues/38)) ([0e1a907](https://github.com/Hazame-hub/alder/commit/0e1a907bd9e1c74fdf05a11a34c17c69d95acbb3))


### Documentation

* describe what 0.8.0 added, and correct the harness entry count ([#46](https://github.com/Hazame-hub/alder/issues/46)) ([d4d10aa](https://github.com/Hazame-hub/alder/commit/d4d10aae7ba3d06d60c2da732dd8f0ecb18799bf))

## [0.7.0](https://github.com/Hazame-hub/alder/compare/v0.6.4...v0.7.0) (2026-09-05)


### Features

* browse users, groups and organizational units as tables ([#30](https://github.com/Hazame-hub/alder/issues/30)) ([c482bfc](https://github.com/Hazame-hub/alder/commit/c482bfc570da977af68deaa6f2d718bc7b77f2c3))
* create entries from the schema, and manage membership as a task ([#33](https://github.com/Hazame-hub/alder/issues/33)) ([40d88fc](https://github.com/Hazame-hub/alder/commit/40d88fc49930c1520f61096c55755cc2095839f8))
* document fields from the schema, and fix the boolean controls ([#32](https://github.com/Hazame-hub/alder/issues/32)) ([d22c19d](https://github.com/Hazame-hub/alder/commit/d22c19d904b09002f6af0072c55e4fe6c5a30000))
* put the location in the URL, and add an overview page ([#36](https://github.com/Hazame-hub/alder/issues/36)) ([f6b77e6](https://github.com/Hazame-hub/alder/commit/f6b77e6351ecaa570f56d6d2152c39a5610de8dc))
* show where a definition came from, and how a password is stored ([#37](https://github.com/Hazame-hub/alder/issues/37)) ([995d8f3](https://github.com/Hazame-hub/alder/commit/995d8f3a33bf4f34fc412851a6d653d40db0057c))

## [0.6.4](https://github.com/Hazame-hub/alder/compare/v0.6.3...v0.6.4) (2026-09-04)


### Fixes

* stop an edit deleting the fields it cannot show ([#28](https://github.com/Hazame-hub/alder/issues/28)) ([ee9f62c](https://github.com/Hazame-hub/alder/commit/ee9f62c500080be9d798b9459267229055aea686))

## [0.6.3](https://github.com/Hazame-hub/alder/compare/v0.6.2...v0.6.3) (2026-09-04)


### Fixes

* allow editing a schema definition that an object class uses ([#26](https://github.com/Hazame-hub/alder/issues/26)) ([d11f21a](https://github.com/Hazame-hub/alder/commit/d11f21af006cf8c79ad8c91c143a8cbe96ee1e3e))

## [0.6.2](https://github.com/Hazame-hub/alder/compare/v0.6.1...v0.6.2) (2026-09-04)


### Fixes

* make the schema entry reachable where it is the schema ([#24](https://github.com/Hazame-hub/alder/issues/24)) ([5c641ab](https://github.com/Hazame-hub/alder/commit/5c641ab7467bf83049aca04f11a1046d9ab5674a))

## [0.6.1](https://github.com/Hazame-hub/alder/compare/v0.6.0...v0.6.1) (2026-09-04)


### Fixes

* report where an added entry went, and explain what a refusal means ([#22](https://github.com/Hazame-hub/alder/issues/22)) ([2118142](https://github.com/Hazame-hub/alder/commit/2118142e220396af48a7c049a90a62b6c882ef6a))

## [0.6.0](https://github.com/Hazame-hub/alder/compare/v0.5.0...v0.6.0) (2026-09-04)


### Features

* warn before a change reaches the server's own configuration ([#20](https://github.com/Hazame-hub/alder/issues/20)) ([961acd9](https://github.com/Hazame-hub/alder/commit/961acd9b16a46ae022f86b4df6fa572f981f8c01))

## [0.5.0](https://github.com/Hazame-hub/alder/compare/v0.4.0...v0.5.0) (2026-09-04)


### Features

* edit the schema, and browse the server's own configuration ([#18](https://github.com/Hazame-hub/alder/issues/18)) ([b63dcd9](https://github.com/Hazame-hub/alder/commit/b63dcd9b665c7155e86be9e568c1c8065273b927))

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
