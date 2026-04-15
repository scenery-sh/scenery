import {
  inferColumns,
  loadStoredDBQueries,
  parseDBParams,
  saveStoredDBQuery,
} from "./db-explorer";

describe("db explorer helpers", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
  });

  it("parses params arrays", () => {
    expect(parseDBParams("[1, \"two\"]")).toEqual([1, "two"]);
    expect(parseDBParams("")).toEqual([]);
  });

  it("stores recent queries without duplicates", () => {
    saveStoredDBQuery("app", "main", { sql: "select 1", paramsText: "[]", arrayMode: false });
    saveStoredDBQuery("app", "main", { sql: "select 1", paramsText: "[]", arrayMode: false });
    saveStoredDBQuery("app", "main", { sql: "select 2", paramsText: "[1]", arrayMode: true });

    const items = loadStoredDBQueries("app", "main");
    expect(items).toHaveLength(2);
    expect(items[0]?.sql).toBe("select 2");
  });

  it("infers columns from object and array rows", () => {
    expect(inferColumns([{ id: 1, name: "a" }])).toEqual(["id", "name"]);
    expect(inferColumns([[1, 2, 3]])).toEqual(["0", "1", "2"]);
  });
});
