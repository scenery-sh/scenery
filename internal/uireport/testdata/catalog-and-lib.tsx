import { QueryTable } from "@scenery/ui";
import { Widget } from "third-party";
import LocalThing from "./LocalThing";

export function Mixed() {
  return (
    <main>
      <QueryTable />
      <Widget />
      <LocalThing />
    </main>
  );
}
