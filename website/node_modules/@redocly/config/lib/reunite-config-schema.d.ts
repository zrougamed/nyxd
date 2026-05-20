export declare const reuniteConfigSchema: {
    readonly type: "object";
    readonly properties: {
        readonly ignoreLint: {
            readonly oneOf: readonly [{
                readonly type: "boolean";
                readonly default: false;
            }, {
                readonly type: "object";
                readonly additionalProperties: {
                    readonly type: "boolean";
                };
            }];
        };
        readonly ignoreLinkChecker: {
            readonly type: "boolean";
        };
        readonly ignoreMarkdocErrors: {
            readonly type: "boolean";
        };
        readonly ignoreRespectMonitoring: {
            readonly type: "boolean";
        };
        readonly jobs: {
            readonly type: "array";
            readonly items: {
                readonly type: "object";
                readonly properties: {
                    readonly path: {
                        readonly type: "string";
                        readonly pattern: "^(?!\\/|\\.\\./)";
                    };
                    readonly agent: {
                        readonly type: "string";
                        readonly enum: readonly ["respect"];
                    };
                    readonly trigger: {
                        readonly oneOf: readonly [{
                            readonly type: "object";
                            readonly properties: {
                                readonly event: {
                                    readonly type: "string";
                                    readonly enum: readonly ["schedule"];
                                };
                                readonly interval: {
                                    readonly type: "string";
                                    readonly enum: readonly ["1m", "2m", "5m", "10m", "15m", "30m", "1h", "3h", "6h", "12h", "1d", "7d"];
                                };
                            };
                            readonly required: readonly ["event"];
                            readonly additionalProperties: false;
                        }, {
                            readonly type: "object";
                            readonly properties: {
                                readonly event: {
                                    readonly type: "string";
                                    readonly enum: readonly ["build"];
                                };
                            };
                            readonly required: readonly ["event"];
                            readonly additionalProperties: false;
                        }];
                    };
                    readonly inputs: {
                        readonly type: "object";
                        readonly additionalProperties: {
                            readonly type: "string";
                        };
                    };
                    readonly servers: {
                        readonly type: "object";
                        readonly additionalProperties: false;
                        readonly patternProperties: {
                            readonly '^[a-zA-Z0-9_-]+$': {
                                readonly type: "string";
                                readonly pattern: "^https?://[^\\s/$.?#].[^\\s]*$";
                            };
                        };
                    };
                    readonly severity: {
                        readonly type: "object";
                        readonly additionalProperties: false;
                        readonly properties: {
                            readonly schemaCheck: {
                                readonly type: "string";
                                readonly enum: readonly ["error", "warn", "off"];
                            };
                            readonly statusCodeCheck: {
                                readonly type: "string";
                                readonly enum: readonly ["error", "warn", "off"];
                            };
                            readonly contentTypeCheck: {
                                readonly type: "string";
                                readonly enum: readonly ["error", "warn", "off"];
                            };
                            readonly successCriteriaCheck: {
                                readonly type: "string";
                                readonly enum: readonly ["error", "warn", "off"];
                            };
                        };
                    };
                    readonly slo: {
                        readonly type: "object";
                        readonly properties: {
                            readonly warn: {
                                readonly type: "number";
                            };
                            readonly error: {
                                readonly type: "number";
                            };
                        };
                        readonly additionalProperties: false;
                    };
                };
                readonly required: readonly ["path", "trigger", "agent"];
                readonly additionalProperties: false;
            };
        };
    };
    readonly additionalProperties: false;
};
