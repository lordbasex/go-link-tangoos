import { describe, expect, it } from "vitest";
import es from "./server.es.json";
import { serverEn } from "./server.en";

const verbs = (s: string) => (s.match(/%(\[\d+\])?[-+#0]*\d*(\.\d+)?[sdvqwxXfgt]/g) ?? []).length;

describe("catálogo del NAS en inglés", () => {
  it("cada texto que puede mandar el NAS tiene traducción, con los mismos verbos", () => {
    const missing = (es as string[]).filter((k) => !(k in serverEn));
    expect(missing).toEqual([]);
    const badVerbs = Object.entries(serverEn).filter(([k, v]) => verbs(k) !== verbs(v)).map(([k]) => k);
    expect(badVerbs).toEqual([]);
  });
});
