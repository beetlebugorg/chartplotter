// viewing-groups.mjs — the S-52 §14.5 viewing-group taxonomy: the standard
// mariner-selectable content groups, each mapping a friendly name to the raw
// S-101 viewing-group number(s) the baker stamps as the per-feature `vg` tile tag
// (internal/engine/portrayal + the native tile57 engine).
//
// Toggling a group OFF adds its `vgs` to mariner.viewingGroupsOff (a DENY-LIST);
// the client's viewingGroupFilter (s52-style.mjs) then hides features whose `vg`
// is in that set. Empty deny-list = every group shown (the default). A feature
// with no `vg` (unbanded, or a tile baked before `vg` existed) always shows.
//
// Only Display Standard (2xxxx) and Display Other (3xxxx/9xxxx) groups are listed:
// Display Base (1xxxx — coastline, safety contour, isolated dangers, land) is the
// mandatory safe-navigation minimum and is NEVER selectable (S-52 §10.2). Ids come
// from portrayal_catalogue.xml <viewingGroups>.
//
// `short` is the compact abbreviation shown on the on-map quick-toggle rail
// (plugins/vg-rail.mjs); the full label/desc stay in the tooltip + Settings tab.

export const VIEWING_GROUP_SECTIONS = [
  {
    id: "depths", label: "Depths & seabed",
    groups: [
      { id: "soundings", label: "Soundings", short: "Snd", desc: "Spot depth soundings", vgs: [33010] },
      { id: "depthContours", label: "Depth contours", short: "Cont", desc: "Depth contours other than the safety contour", vgs: [33020] },
      { id: "seabed", label: "Seabed & quality", short: "Sbd", desc: "Nature of seabed, springs, weed/kelp", vgs: [34010, 34020] },
      { id: "subseaDangers", label: "Rocks, wrecks & obstructions", short: "Wrk", desc: "Non-dangerous underwater rocks, wrecks & obstructions", vgs: [34050, 34051] },
      { id: "cables", label: "Cables & pipelines", short: "Cbl", desc: "Submarine cables & pipelines, mooring cables", vgs: [34030, 34070, 24010] },
    ],
  },
  {
    id: "aids", label: "Buoys, beacons & lights",
    groups: [
      { id: "buoys", label: "Buoys", short: "Buoy", desc: "Buoys, light floats & mooring buoys", vgs: [27010, 27011] },
      { id: "beacons", label: "Beacons", short: "Bcn", desc: "Beacons of all categories", vgs: [27020] },
      { id: "marks", label: "Daymarks & topmarks", short: "Mrk", desc: "Daymarks, topmarks, distance marks", vgs: [27025, 27030, 27050] },
      { id: "lights", label: "Lights", short: "Lt", desc: "Lights & their sectors", vgs: [27070] },
      { id: "fogSignals", label: "Fog signals", short: "Fog", desc: "Fog signals & retro-reflectors", vgs: [27080] },
      { id: "radarAids", label: "Radar beacons", short: "Rcn", desc: "Racons, radar reflectors & ranges", vgs: [27200, 27210, 27220, 27230, 27240] },
    ],
  },
  {
    id: "areas", label: "Areas & limits",
    groups: [
      { id: "restricted", label: "Restricted areas", short: "Rstr", desc: "Restricted, prohibited & protected areas", vgs: [26010, 26200, 26210] },
      { id: "caution", label: "Caution areas", short: "Ctn", desc: "Caution areas", vgs: [26150] },
      { id: "anchorages", label: "Anchorages", short: "Anch", desc: "Anchorage areas & anchor berths", vgs: [26220] },
      { id: "dumping", label: "Dumping grounds", short: "Dmp", desc: "Dumping grounds & spoil areas", vgs: [26240] },
      { id: "military", label: "Military & transit areas", short: "Mil", desc: "Submarine transit lanes, military practice areas", vgs: [26040] },
      { id: "production", label: "Production & cargo areas", short: "Prod", desc: "Cargo transhipment, incineration, production areas", vgs: [26250] },
    ],
  },
  {
    id: "routes", label: "Routes & traffic",
    groups: [
      { id: "leadingLines", label: "Leading & clearing lines", short: "Ldg", desc: "Leading lines, clearing lines, traffic lanes, deep-water routes", vgs: [25010] },
      { id: "tracks", label: "Recommended tracks", short: "Trk", desc: "Recommended tracks, traffic lanes & routes", vgs: [25020] },
      { id: "ferry", label: "Ferry routes", short: "Fry", desc: "Ferry routes", vgs: [25030] },
      { id: "fairways", label: "Fairways", short: "Fwy", desc: "Fairways", vgs: [26050] },
      { id: "archipelagic", label: "Archipelagic sea lanes", short: "ASL", desc: "Archipelagic sea lanes", vgs: [26260] },
    ],
  },
  {
    id: "land", label: "Land & port",
    groups: [
      { id: "builtUp", label: "Built-up areas", short: "Bldg", desc: "Built-up areas", vgs: [22240] },
      { id: "landFeatures", label: "Land features", short: "Land", desc: "Slopes, hills, vegetation, rivers & lakes", vgs: [32010, 32030, 32050] },
      { id: "shoreStructures", label: "Shore & port structures", short: "Shor", desc: "Shore structures, harbour & port features", vgs: [32200, 32220, 32400, 32410, 32440] },
      { id: "airports", label: "Airports", short: "Apt", desc: "Airports & runways", vgs: [32240] },
    ],
  },
  {
    id: "services", label: "Services",
    groups: [
      { id: "stations", label: "Pilot & signal stations", short: "Plt", desc: "Pilot boarding points, signal & traffic stations", vgs: [28010, 28020] },
      { id: "radioStations", label: "Radio, radar & coastguard", short: "Rdo", desc: "Radio/radar stations, coastguard & rescue stations", vgs: [38010, 38030] },
      { id: "smallCraft", label: "Small-craft facilities", short: "SCF", desc: "Small-craft facilities", vgs: [38200, 38210] },
    ],
  },
  {
    id: "admin", label: "Administrative",
    groups: [
      { id: "magVar", label: "Magnetic variation", short: "Mag", desc: "Magnetic variation & local anomalies", vgs: [31080] },
      { id: "zones", label: "Maritime zones", short: "Zone", desc: "Fishery zones, EEZ, contiguous zones, pollution areas", vgs: [36040, 36050, 36060] },
      { id: "tidal", label: "Tidal & current info", short: "Tide", desc: "Tidal levels & tidal-stream / current information", vgs: [33050, 33060] },
    ],
  },
];

// groupId → [vg ids], for the settings get/set (core-settings.mjs).
export const VG_BY_GROUP_ID = Object.fromEntries(
  VIEWING_GROUP_SECTIONS.flatMap((s) => s.groups.map((g) => [g.id, g.vgs])),
);
