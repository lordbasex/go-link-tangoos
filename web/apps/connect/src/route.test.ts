import { describe, expect, it } from "vitest";
import { route } from "./App";

describe("rutas", () => {
  it("reconoce cada página", () => {
    expect(route("/").page).toBe("landing");
    expect(route("/demo").page).toBe("demo");
    expect(route("/nas/").page).toBe("nas");
    expect(route("/nas/vincular?code=1").page).toBe("link");
    expect(route("/nas/11111111-1111-4111-8111-111111111111")).toEqual({ page: "remote", id: "11111111-1111-4111-8111-111111111111" });
    expect(route("/s/11111111-1111-4111-8111-111111111111.0123456789abcdef")).toEqual({ page: "share", id: "11111111-1111-4111-8111-111111111111.0123456789abcdef" });
    expect(route("/s/AbCdEfGhIjKlMnOpQrStUv").page).toBe("notfound");
    expect(route("/s/../../etc").page).toBe("notfound");
  });
});
