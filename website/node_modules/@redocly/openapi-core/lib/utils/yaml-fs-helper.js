import * as fs from 'node:fs';
import * as path from 'node:path';
import { parseYaml } from '../js-yaml/index.js';
import { isAbsoluteUrl } from '../ref-utils.js';
export { parseYaml, stringifyYaml } from '../js-yaml/index.js';
export async function loadYaml(filename) {
    const contents = await fs.promises.readFile(filename, 'utf-8');
    return parseYaml(contents);
}
export function yamlAndJsonSyncReader(filePath) {
    const content = fs.readFileSync(filePath, 'utf-8');
    return parseYaml(content);
}
export function resolveRelativePath(filePath, base) {
    if (isAbsoluteUrl(filePath) || base === undefined) {
        return filePath;
    }
    return path.resolve(path.dirname(base), filePath);
}
export function readFileAsStringSync(filePath) {
    return fs.readFileSync(filePath, 'utf-8');
}
//# sourceMappingURL=yaml-fs-helper.js.map