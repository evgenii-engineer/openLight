# make/release.mk
#
# Release helpers. Today this is just a shortcut that builds every binary
# we ship (Pi + Mac mini, agent + CLI) so a tag cut can attach them to a
# GitHub release. Extend with checksums / signing / artifact uploads as
# the release process grows.

##@ Release

.PHONY: release release-rpi release-macmini

release: release-rpi release-macmini ## Build all release binaries (Pi + Mac mini, agent + CLI)

release-rpi: build-rpi build-rpi-cli ## Build Raspberry Pi agent + CLI

release-macmini: build-macmini build-macmini-cli ## Build Mac mini agent + CLI
