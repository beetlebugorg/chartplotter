// archive-store.mjs — a one-slot IndexedDB store for the current .pmtiles archive
// (kept as a Blob). Works on a plain-http LAN device — unlike OPFS, no secure
// context needed. Holds the most recently baked/uploaded archive, reloaded on boot.

const ARCHIVE_DB = "chartplotter-archive";

function archiveDB() {
  return new Promise((res, rej) => {
    const r = indexedDB.open(ARCHIVE_DB, 1);
    r.onupgradeneeded = () => r.result.createObjectStore("archive");
    r.onsuccess = () => res(r.result);
    r.onerror = () => rej(r.error);
  });
}

export async function archivePut(blob) {
  const db = await archiveDB();
  try {
    await new Promise((res, rej) => {
      const tx = db.transaction("archive", "readwrite");
      tx.objectStore("archive").put(blob, "current");
      tx.oncomplete = res;
      tx.onerror = () => rej(tx.error);
    });
  } finally { db.close(); }
}

export async function archiveGet() {
  const db = await archiveDB();
  try {
    return await new Promise((res, rej) => {
      const tx = db.transaction("archive", "readonly");
      const rq = tx.objectStore("archive").get("current");
      rq.onsuccess = () => res(rq.result || null);
      rq.onerror = () => rej(rq.error);
    });
  } finally { db.close(); }
}
