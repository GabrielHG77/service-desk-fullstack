// Embute src/ui.html no bundle: o .exe portátil não pode depender de um
// arquivo ao lado dele.
import { readFileSync, writeFileSync } from 'node:fs'

const html = readFileSync(new URL('../src/ui.html', import.meta.url), 'utf8')
writeFileSync(new URL('../src/ui.gen.js', import.meta.url), `export default ${JSON.stringify(html)}\n`)
