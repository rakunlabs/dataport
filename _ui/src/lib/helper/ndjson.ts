// readNdjson calls onLine with each JSON value of a newline-delimited JSON
// stream, as it arrives. A last line without a newline counts too.
export async function readNdjson<T>(body: ReadableStream<Uint8Array<ArrayBuffer>>, onLine: (value: T) => void): Promise<void> {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = '';
  const flush = (line: string) => {
    line = line.trim();
    if (line) onLine(JSON.parse(line) as T);
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    let nl: number;
    while ((nl = buf.indexOf('\n')) >= 0) {
      flush(buf.slice(0, nl));
      buf = buf.slice(nl + 1);
    }
  }
  flush(buf);
}
