#!/usr/bin/env python3
"""Build an anonymous demo workspace for crux's README previews."""
import os, shutil, subprocess

ROOT = "/var/tmp/crux-demo/home"
P = os.path.join(ROOT, "projects")
shutil.rmtree("/var/tmp/crux-demo", ignore_errors=True)
os.makedirs(P)


def w(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text.lstrip("\n"))


# ledger: a Rust crate with 400 tests, two of them failing, and a warning.
# Each test sleeps a little so the live status line has something to show.
L = os.path.join(P, "ledger")
w(f"{L}/Cargo.toml", """
[package]
name = "ledger"
version = "0.4.0"
edition = "2021"

[dependencies]
""")
src = ['''
//! Money amounts in integer cents.

pub fn parse_cents(s: &str) -> Option<i64> {
    let s = s.trim();
    let (neg, s) = match s.strip_prefix('-') { Some(r) => (true, r), None => (false, s) };
    let (whole, frac) = s.split_once('.').unwrap_or((s, "0"));
    let frac = format!("{:0<2}", frac);
    let v = whole.parse::<i64>().ok()? * 100 + frac[..2].parse::<i64>().ok()?;
    Some(if neg { -v } else { v })
}

pub fn format_cents(c: i64) -> String {
    let sign = if c < 0 { "-" } else { "" };
    format!("{}{}.{:02}", sign, c.abs() / 100, c.abs() % 100)
}

/// Splits an amount into n parts that differ by at most one cent.
pub fn split_even(total: i64, n: i64) -> Vec<i64> {
    let base = total / n;
    let rest = total % n;
    let rounding = 0;
    (0..n).map(|i| base + if i < rest { 1 } else { 0 }).collect()
}

pub fn apply_rate(c: i64, basis_points: i64) -> i64 {
    c * basis_points / 10_000
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{thread, time::Duration};

    fn tick() { thread::sleep(Duration::from_millis(70)); }
''']
for i in range(100):
    src.append(f"    #[test] fn parse_roundtrip_{i}() {{ tick(); assert_eq!(parse_cents(&format_cents({i * 37 - 900})), Some({i * 37 - 900})); }}")
for i in range(100):
    src.append(f"    #[test] fn format_{i}() {{ tick(); assert_eq!(format_cents({i * 101}), \"{i * 101 // 100}.{i * 101 % 100:02}\"); }}")
for i in range(1, 101):
    src.append(f"    #[test] fn split_sums_{i}() {{ tick(); assert_eq!(split_even({i * 997}, {i % 7 + 1}).iter().sum::<i64>(), {i * 997}); }}")
for i in range(98):
    src.append(f"    #[test] fn rate_{i}() {{ tick(); assert_eq!(apply_rate(10_000, {i * 25}), {i * 25}); }}")
src.append('''    #[test] fn split_negative_total() {
        tick();
        let parts = split_even(-1001, 3);
        assert_eq!(parts, vec![-334, -334, -333], "parts of -10.01 split three ways");
    }
    #[test] fn rate_rounds_half_up() {
        tick();
        assert_eq!(apply_rate(1_005, 5_000), 503, "50% of 10.05 should round up");
    }
}
''')
w(f"{L}/src/lib.rs", "\n".join(src))

# atlas: a Python package with a pytest suite, two failures and a warning
A = os.path.join(P, "atlas")
w(f"{A}/atlas/__init__.py", "")
w(f"{A}/atlas/geo.py", '''
import math, warnings

def haversine_km(a, b):
    lat1, lon1, lat2, lon2 = map(math.radians, (*a, *b))
    d = math.sin((lat2 - lat1) / 2) ** 2 + math.cos(lat1) * math.cos(lat2) * math.sin((lon2 - lon1) / 2) ** 2
    return 2 * 6371 * math.asin(math.sqrt(d))

def bbox(points):
    lats = [p[0] for p in points]
    lons = [p[1] for p in points]
    return (min(lats), min(lons), max(lats), max(lons))

def parse_coord(s):
    lat, lon = s.split(",")
    return float(lat), float(lon)

def legacy_round(x):
    warnings.warn("legacy_round() is deprecated, use round()", DeprecationWarning)
    return round(x, 3)
''')
tests = ['''
import pytest
from atlas.geo import haversine_km, bbox, parse_coord, legacy_round

BERLIN, PARIS = (52.52, 13.405), (48.8566, 2.3522)

def test_berlin_paris():
    assert round(haversine_km(BERLIN, PARIS)) == 877

def test_same_point():
    assert haversine_km(BERLIN, BERLIN) == 0

def test_bbox_crosses_antimeridian():
    box = bbox([(10.0, 179.5), (12.0, -179.5)])
    assert box == (10.0, 179.5, 12.0, -179.5)

def test_parse_with_spaces():
    assert parse_coord(" 52.52 ; 13.405 ") == BERLIN

def test_legacy_round():
    assert legacy_round(1.23456) == 1.235
''']
for i in range(120):
    tests.append(f"def test_parse_{i}():\n    assert parse_coord('{i % 90}.5,{i % 180}.25') == ({i % 90}.5, {i % 180}.25)\n")
w(f"{A}/tests/test_geo.py", "\n".join(tests))
w(f"{A}/pyproject.toml", '[project]\nname = "atlas"\nversion = "1.2.0"\n\n[tool.pytest.ini_options]\npythonpath = ["."]\n')

# build once so the recording doesn't wait for the compiler
env = dict(os.environ, HOME=ROOT, RUSTUP_HOME=os.path.expanduser("~/.rustup"), CARGO_HOME=os.path.expanduser("~/.cargo"))
subprocess.run(["cargo", "test", "--no-run", "-q"], cwd=L, env=env, check=True, capture_output=True)
print("demo workspace ready in", P)
