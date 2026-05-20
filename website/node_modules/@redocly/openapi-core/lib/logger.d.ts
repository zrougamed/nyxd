import * as colorette from 'colorette';
export declare const colorOptions: {
    enabled: boolean;
};
export declare const colorize: typeof colorette;
export interface LoggerInterface {
    info(message: string): void;
    warn(message: string): void;
    output(message: string): void;
    error(message: string): void;
    printNewLine(): void;
    printSeparator(separator: string): void;
    indent(str: string, level: number): string;
}
declare class Logger implements LoggerInterface {
    info(str: string): boolean | void;
    warn(str: string): boolean | void;
    error(str: string): boolean | void;
    output(str: string): boolean | void;
    printNewLine(): void;
    printSeparator(separator: string): boolean | void;
    indent(str: string, level: number): string;
}
export declare const logger: Logger;
export {};
//# sourceMappingURL=logger.d.ts.map