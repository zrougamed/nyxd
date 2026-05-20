export { parseYaml, stringifyYaml } from '../js-yaml/index.js';
export declare function loadYaml<T>(filename: string): Promise<T>;
export declare function yamlAndJsonSyncReader<T>(filePath: string): T;
export declare function resolveRelativePath(filePath: string, base?: string): string;
export declare function readFileAsStringSync(filePath: string): string;
//# sourceMappingURL=yaml-fs-helper.d.ts.map