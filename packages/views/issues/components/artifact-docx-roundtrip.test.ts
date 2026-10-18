// @vitest-environment jsdom
import { inflateRawSync } from "node:zlib";
import { createDocxEditor } from "@docx-editor.dev/core";
import { describe, expect, it } from "vitest";

function zipStored(files: Record<string, string>): Uint8Array {
  const locals: Uint8Array[] = [];
  const centrals: Uint8Array[] = [];
  let offset = 0;
  for (const [name, body] of Object.entries(files)) {
    const nameBytes = new TextEncoder().encode(name);
    const data = new TextEncoder().encode(body);
    const local = new Uint8Array(30);
    const view = new DataView(local.buffer);
    view.setUint32(0, 0x04034b50, true);
    view.setUint16(4, 20, true);
    view.setUint16(8, 0, true);
    view.setUint32(14, crc32(data), true);
    view.setUint32(18, data.length, true);
    view.setUint32(22, data.length, true);
    view.setUint16(26, nameBytes.length, true);
    locals.push(concat(local, nameBytes, data));
    const central = new Uint8Array(46);
    const centralView = new DataView(central.buffer);
    centralView.setUint32(0, 0x02014b50, true);
    centralView.setUint16(4, 20, true);
    centralView.setUint16(6, 20, true);
    centralView.setUint32(16, crc32(data), true);
    centralView.setUint32(20, data.length, true);
    centralView.setUint32(24, data.length, true);
    centralView.setUint16(28, nameBytes.length, true);
    centralView.setUint32(42, offset, true);
    centrals.push(concat(central, nameBytes));
    offset += 30 + nameBytes.length + data.length;
  }
  const centralDir = concat(...centrals);
  const end = new Uint8Array(22);
  const endView = new DataView(end.buffer);
  endView.setUint32(0, 0x06054b50, true);
  endView.setUint16(8, Object.keys(files).length, true);
  endView.setUint16(10, Object.keys(files).length, true);
  endView.setUint32(12, centralDir.length, true);
  endView.setUint32(16, offset, true);
  return concat(...locals, centralDir, end);
}

function concat(...parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((sum, part) => sum + part.length, 0));
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

function crc32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function unzip(raw: ArrayBuffer): Record<string, Uint8Array> {
  const data = new Uint8Array(raw);
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let eocd = -1;
  for (let i = data.length - 22; i >= 0; i -= 1) {
    if (view.getUint32(i, true) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error("zip end is missing");
  const count = view.getUint16(eocd + 10, true);
  let ptr = view.getUint32(eocd + 16, true);
  const out: Record<string, Uint8Array> = {};
  for (let n = 0; n < count; n += 1) {
    if (view.getUint32(ptr, true) !== 0x02014b50) throw new Error("zip directory is invalid");
    const method = view.getUint16(ptr + 10, true);
    const compSize = view.getUint32(ptr + 20, true);
    const nameLen = view.getUint16(ptr + 28, true);
    const extraLen = view.getUint16(ptr + 30, true);
    const commentLen = view.getUint16(ptr + 32, true);
    const localOff = view.getUint32(ptr + 42, true);
    const name = new TextDecoder().decode(data.subarray(ptr + 46, ptr + 46 + nameLen));
    const localNameLen = view.getUint16(localOff + 26, true);
    const localExtraLen = view.getUint16(localOff + 28, true);
    const start = localOff + 30 + localNameLen + localExtraLen;
    const comp = data.subarray(start, start + compSize);
    out[name] = method === 0 ? comp : inflateRawSync(comp);
    ptr += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

function goldenDocx(): Uint8Array {
  return zipStored({
    "[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/customXml/item1.xml" ContentType="application/xml"/>
</Types>`,
    "_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
    "word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/customXml" Target="../customXml/item1.xml"/>
</Relationships>`,
    "word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>Second page</w:t></w:r></w:p>
    <w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr>
  </w:body>
</w:document>`,
    "customXml/item1.xml": `<?xml version="1.0" encoding="UTF-8"?><root>KEEP-PART</root>`,
  });
}

describe("docx editor round trip", () => {
  it("keeps text, a page break, and an extra package part", async () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const editor = createDocxEditor({ container: host, document: goldenDocx() });
    await new Promise((resolve) => setTimeout(resolve, 300));
    const edited = editor.exec({ type: "insertText", text: "Edited " } as never);
    expect(edited.ok).toBe(true);
    const saved = await editor.save();
    editor.destroy();
    host.remove();
    expect(saved).toBeTruthy();
    const files = unzip(saved as ArrayBuffer);
    const documentXml = new TextDecoder().decode(files["word/document.xml"] ?? new Uint8Array());
    expect(documentXml).toContain("Hello artifact");
    expect(documentXml).toContain("Second page");
    expect(documentXml).toContain("Edited ");
    expect(documentXml).toMatch(/w:type="page"|w:type='page'/);
    expect(new TextDecoder().decode(files["customXml/item1.xml"] ?? new Uint8Array())).toContain("KEEP-PART");
  });
});
