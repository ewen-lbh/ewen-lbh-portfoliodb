build:
	#!/usr/bin/env bash
	set -euxo pipefail
	cd cmd/ortfodb
	go mod tidy
	go build
	mv ortfodb ../../

install:
	just build
	cp ortfodb ~/.local/bin/ortfodb
	chmod +x ~/.local/bin/ortfodb

prepare-release $VERSION:
	./tools/update_meta_go.py $VERSION
	just build
	./tools/generate_schemas.py
	./tools/build_readme.py
	just build-packages $VERSION

release name='${version}':
	GITHUB_TOKEN=$(rbw get 'GitHub VSCode PAT') release-it --github.releaseName={{quote(name)}}
	cd packages/python; poetry publish
	cd packages/typescript; npm publish
	cd packages/rust; cargo publish

build-packages version:
	just build-typescript {{version}}
	just build-python {{version}}
	just build-rust {{version}}

build-typescript version:
	#!/usr/bin/env bash
	set -euxo pipefail
	for schema in schemas/*.schema.json; do
		quicktype --src-lang schema -l typescript $schema -o packages/typescript/src/$(basename $schema .schema.json).ts
	done
	cd packages/typescript
	jq '.version = "{{ version }}"' < package.json | sponge package.json
	rm -r dist
	npm run build

build-python version:
	#!/usr/bin/env bash
	set -euxo pipefail
	for schema in schemas/*.schema.json; do
		quicktype --src-lang schema -l python $schema -o packages/python/ortfodb/$(basename $schema .schema.json).py
	done
	cd packages/python
	poetry version {{ version }}
	poetry build

build-rust version:
	#!/usr/bin/env bash
	set -euxo pipefail
	for schema in schemas/*.schema.json; do
		quicktype --src-lang schema -l rust --visibility public $schema -o packages/rust/src/$(basename $schema .schema.json).rs
		sed -i 's/generated_module/ortfodb/g' packages/rust/src/$(basename $schema .schema.json).rs
	done
	cd packages/rust
	tomlq -ti '.package.version = "{{ version }}"' Cargo.toml
	cargo build
