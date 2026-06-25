// Logic tests for the date-dependent display period (S-52 §10.4.1.1), the
// reference _inDatePeriod that the dateFilter MapLibre expression mirrors.
// Run: node --test web/src/chart-canvas/s52-style.date.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { _inDatePeriod, dateFilter } from "./s52-style.mjs";

const seasonalSummer = { date_recurring: 1, date_start: "0315", date_end: "1201" }; // Slaughter Creek buoy
const seasonalWinter = { date_recurring: 1, date_start: "1101", date_end: "0315" }; // wraps the year
const fixedRange = { date_recurring: 0, date_start: "20240101", date_end: "20241231" };
const openStart = { date_recurring: 1, date_start: "0401" }; // on station from Apr, no end
const openEnd = { date_recurring: 1, date_end: "1115" }; // until mid-Nov
const undated = {}; // not date-dependent

test("recurring summer range — in vs out of season", () => {
  assert.equal(_inDatePeriod(seasonalSummer, "20260624", "0624"), true, "June is in season");
  assert.equal(_inDatePeriod(seasonalSummer, "20260101", "0101"), false, "January is out");
  assert.equal(_inDatePeriod(seasonalSummer, "20261215", "1215"), false, "mid-December is out");
  assert.equal(_inDatePeriod(seasonalSummer, "20260315", "0315"), true, "start day inclusive");
  assert.equal(_inDatePeriod(seasonalSummer, "20261201", "1201"), true, "end day inclusive");
});

test("recurring winter range — year wrap", () => {
  assert.equal(_inDatePeriod(seasonalWinter, "20261215", "1215"), true, "December is in (>= start)");
  assert.equal(_inDatePeriod(seasonalWinter, "20260101", "0101"), true, "January is in (<= end)");
  assert.equal(_inDatePeriod(seasonalWinter, "20260624", "0624"), false, "June is out");
});

test("fixed full-date range compares YYYYMMDD", () => {
  assert.equal(_inDatePeriod(fixedRange, "20240615", "0615"), true, "2024 mid-year is in");
  assert.equal(_inDatePeriod(fixedRange, "20260624", "0624"), false, "2026 is after the 2024 range");
});

test("semi-open ranges", () => {
  assert.equal(_inDatePeriod(openStart, "20260624", "0624"), true, "after start, no end");
  assert.equal(_inDatePeriod(openStart, "20260201", "0201"), false, "before start");
  assert.equal(_inDatePeriod(openEnd, "20260624", "0624"), true, "before end, no start");
  assert.equal(_inDatePeriod(openEnd, "20261215", "1215"), false, "after end");
});

test("undated features always show", () => {
  assert.equal(_inDatePeriod(undated, "20260624", "0624"), true);
});

test("dateFilter builds a valid expression array with the viewing date pinned", () => {
  const f = dateFilter({ dateView: "20260624" });
  assert.equal(Array.isArray(f), true);
  assert.equal(f[0], "any");
  // today's parts must appear as string literals in the expression
  const flat = JSON.stringify(f);
  assert.match(flat, /"20260624"/);
  assert.match(flat, /"0624"/);
});
