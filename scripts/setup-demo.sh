#!/bin/sh
# Populate a local workspace with actual, read-only example repositories.
set -eu
cd "$(dirname "$0")/.."
mkdir -p repos
clone_repo() {
  if [ ! -d "repos/$1.git" ]; then
    git clone --bare "$2" "repos/$1.git"
    printf '%s\n' "$3" > "repos/$1.git/description"
  fi
}
clone_repo babyjubjub-rs https://github.com/arnaucube/babyjubjub-rs.git 'BabyJubJub elliptic curve implementation and EdDSA signature scheme in Rust'
clone_repo protogalaxy-poc https://github.com/arnaucube/protogalaxy-poc.git 'ProtoGalaxy folding scheme PoC implementation https://eprint.iacr.org/2023/1106.pdf'
clone_repo fri-commitment https://github.com/arnaucube/fri-commitment.git 'FRI low-degree-testing & polynomial commitment scheme'
clone_repo go-snark https://github.com/arnaucube/go-snark.git 'zkSNARK library implementation in Go from scratch (compiler, setup, prover, verifier)'
