export declare const entityCatalogExcludeSchema: {
    readonly type: "object";
    readonly required: readonly ["key"];
    readonly properties: {
        readonly key: {
            readonly type: "string";
        };
    };
    readonly additionalProperties: false;
};
export declare const entityCatalogIncludeSchema: {
    readonly type: "object";
    readonly required: readonly ["type"];
    readonly properties: {
        readonly type: {
            readonly type: "string";
        };
    };
    readonly additionalProperties: false;
};
export declare const entityCatalogFilterSchema: {
    readonly type: "object";
    readonly required: readonly ["property", "title"];
    readonly properties: {
        readonly property: {
            readonly type: "string";
        };
        readonly hide: {
            readonly type: "boolean";
        };
        readonly label: {
            readonly type: "string";
        };
        readonly options: {
            readonly type: "array";
            readonly items: {
                readonly type: "string";
            };
        };
        readonly type: {
            readonly type: "string";
            readonly enum: readonly ["select", "checkboxes", "date-range"];
            readonly default: "checkboxes";
        };
        readonly title: {
            readonly type: "string";
        };
        readonly titleTranslationKey: {
            readonly type: "string";
        };
        readonly parentFilter: {
            readonly type: "string";
        };
        readonly valuesMapping: {
            readonly type: "object";
            readonly additionalProperties: {
                readonly type: "string";
            };
        };
    };
    readonly additionalProperties: false;
};
export declare const entityCatalogSpecificCatalogSchema: {
    readonly type: "object";
    readonly properties: {
        readonly slug: {
            readonly type: "string";
        };
        readonly hide: {
            readonly type: "boolean";
        };
        readonly includes: {
            readonly type: "array";
            readonly items: {
                readonly type: "object";
                readonly required: readonly ["type"];
                readonly properties: {
                    readonly type: {
                        readonly type: "string";
                    };
                };
                readonly additionalProperties: false;
            };
        };
        readonly excludes: {
            readonly type: "array";
            readonly items: {
                readonly type: "object";
                readonly required: readonly ["key"];
                readonly properties: {
                    readonly key: {
                        readonly type: "string";
                    };
                };
                readonly additionalProperties: false;
            };
        };
        readonly filters: {
            readonly type: "array";
            readonly items: {
                readonly type: "object";
                readonly required: readonly ["property", "title"];
                readonly properties: {
                    readonly property: {
                        readonly type: "string";
                    };
                    readonly hide: {
                        readonly type: "boolean";
                    };
                    readonly label: {
                        readonly type: "string";
                    };
                    readonly options: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "string";
                        };
                    };
                    readonly type: {
                        readonly type: "string";
                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                        readonly default: "checkboxes";
                    };
                    readonly title: {
                        readonly type: "string";
                    };
                    readonly titleTranslationKey: {
                        readonly type: "string";
                    };
                    readonly parentFilter: {
                        readonly type: "string";
                    };
                    readonly valuesMapping: {
                        readonly type: "object";
                        readonly additionalProperties: {
                            readonly type: "string";
                        };
                    };
                };
                readonly additionalProperties: false;
            };
        };
        readonly titleTranslationKey: {
            readonly type: "string";
        };
        readonly descriptionTranslationKey: {
            readonly type: "string";
        };
        readonly catalogSwitcherLabelTranslationKey: {
            readonly type: "string";
        };
    };
    readonly additionalProperties: false;
};
export declare const entityCatalogMetadataSchemaPropertySchema: {
    readonly type: "object";
    readonly properties: {
        readonly type: {
            readonly type: "string";
            readonly enum: readonly ["string", "number", "boolean", "array", "object"];
        };
        readonly description: {
            readonly type: "string";
        };
        readonly example: {
            readonly oneOf: readonly [{
                readonly type: "string";
            }, {
                readonly type: "number";
            }, {
                readonly type: "boolean";
            }, {
                readonly type: "array";
            }, {
                readonly type: "object";
            }];
        };
        readonly enum: {
            readonly type: "array";
            readonly items: {
                readonly type: "string";
            };
        };
        readonly pattern: {
            readonly type: "string";
        };
        readonly format: {
            readonly type: "string";
        };
        readonly minimum: {
            readonly type: "number";
        };
        readonly maximum: {
            readonly type: "number";
        };
        readonly items: {
            readonly type: "object";
        };
    };
    readonly additionalProperties: true;
};
export declare const entityCatalogMetadataSchema: {
    readonly type: "object";
    readonly required: readonly ["type", "properties"];
    readonly properties: {
        readonly type: {
            readonly type: "string";
            readonly enum: readonly ["object"];
        };
        readonly description: {
            readonly type: "string";
        };
        readonly properties: {
            readonly type: "object";
            readonly additionalProperties: {
                readonly type: "object";
                readonly properties: {
                    readonly type: {
                        readonly type: "string";
                        readonly enum: readonly ["string", "number", "boolean", "array", "object"];
                    };
                    readonly description: {
                        readonly type: "string";
                    };
                    readonly example: {
                        readonly oneOf: readonly [{
                            readonly type: "string";
                        }, {
                            readonly type: "number";
                        }, {
                            readonly type: "boolean";
                        }, {
                            readonly type: "array";
                        }, {
                            readonly type: "object";
                        }];
                    };
                    readonly enum: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "string";
                        };
                    };
                    readonly pattern: {
                        readonly type: "string";
                    };
                    readonly format: {
                        readonly type: "string";
                    };
                    readonly minimum: {
                        readonly type: "number";
                    };
                    readonly maximum: {
                        readonly type: "number";
                    };
                    readonly items: {
                        readonly type: "object";
                    };
                };
                readonly additionalProperties: true;
            };
        };
        readonly required: {
            readonly type: "array";
            readonly items: {
                readonly type: "string";
            };
        };
        readonly additionalProperties: {
            readonly type: "boolean";
        };
    };
    readonly additionalProperties: true;
};
export declare const entityCatalogEntityTypeSchema: {
    readonly type: "object";
    readonly required: readonly ["name", "description", "metadataSchema"];
    readonly properties: {
        readonly name: {
            readonly type: "string";
            readonly description: "Display name of the entity type";
        };
        readonly description: {
            readonly type: "string";
            readonly description: "Description of the entity type";
        };
        readonly metadataSchema: {
            readonly type: "object";
            readonly required: readonly ["type", "properties"];
            readonly properties: {
                readonly type: {
                    readonly type: "string";
                    readonly enum: readonly ["object"];
                };
                readonly description: {
                    readonly type: "string";
                };
                readonly properties: {
                    readonly type: "object";
                    readonly additionalProperties: {
                        readonly type: "object";
                        readonly properties: {
                            readonly type: {
                                readonly type: "string";
                                readonly enum: readonly ["string", "number", "boolean", "array", "object"];
                            };
                            readonly description: {
                                readonly type: "string";
                            };
                            readonly example: {
                                readonly oneOf: readonly [{
                                    readonly type: "string";
                                }, {
                                    readonly type: "number";
                                }, {
                                    readonly type: "boolean";
                                }, {
                                    readonly type: "array";
                                }, {
                                    readonly type: "object";
                                }];
                            };
                            readonly enum: {
                                readonly type: "array";
                                readonly items: {
                                    readonly type: "string";
                                };
                            };
                            readonly pattern: {
                                readonly type: "string";
                            };
                            readonly format: {
                                readonly type: "string";
                            };
                            readonly minimum: {
                                readonly type: "number";
                            };
                            readonly maximum: {
                                readonly type: "number";
                            };
                            readonly items: {
                                readonly type: "object";
                            };
                        };
                        readonly additionalProperties: true;
                    };
                };
                readonly required: {
                    readonly type: "array";
                    readonly items: {
                        readonly type: "string";
                    };
                };
                readonly additionalProperties: {
                    readonly type: "boolean";
                };
            };
            readonly additionalProperties: true;
        };
        readonly icon: {
            readonly type: "object";
            readonly properties: {
                readonly src: {
                    readonly type: "string";
                };
                readonly srcSet: {
                    readonly type: "string";
                };
            };
            readonly additionalProperties: false;
        };
    };
    readonly additionalProperties: false;
};
export declare const entityCatalogEntityTypesSchema: {
    readonly type: "object";
    readonly additionalProperties: {
        readonly type: "object";
        readonly required: readonly ["name", "description", "metadataSchema"];
        readonly properties: {
            readonly name: {
                readonly type: "string";
                readonly description: "Display name of the entity type";
            };
            readonly description: {
                readonly type: "string";
                readonly description: "Description of the entity type";
            };
            readonly metadataSchema: {
                readonly type: "object";
                readonly required: readonly ["type", "properties"];
                readonly properties: {
                    readonly type: {
                        readonly type: "string";
                        readonly enum: readonly ["object"];
                    };
                    readonly description: {
                        readonly type: "string";
                    };
                    readonly properties: {
                        readonly type: "object";
                        readonly additionalProperties: {
                            readonly type: "object";
                            readonly properties: {
                                readonly type: {
                                    readonly type: "string";
                                    readonly enum: readonly ["string", "number", "boolean", "array", "object"];
                                };
                                readonly description: {
                                    readonly type: "string";
                                };
                                readonly example: {
                                    readonly oneOf: readonly [{
                                        readonly type: "string";
                                    }, {
                                        readonly type: "number";
                                    }, {
                                        readonly type: "boolean";
                                    }, {
                                        readonly type: "array";
                                    }, {
                                        readonly type: "object";
                                    }];
                                };
                                readonly enum: {
                                    readonly type: "array";
                                    readonly items: {
                                        readonly type: "string";
                                    };
                                };
                                readonly pattern: {
                                    readonly type: "string";
                                };
                                readonly format: {
                                    readonly type: "string";
                                };
                                readonly minimum: {
                                    readonly type: "number";
                                };
                                readonly maximum: {
                                    readonly type: "number";
                                };
                                readonly items: {
                                    readonly type: "object";
                                };
                            };
                            readonly additionalProperties: true;
                        };
                    };
                    readonly required: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: {
                        readonly type: "boolean";
                    };
                };
                readonly additionalProperties: true;
            };
            readonly icon: {
                readonly type: "object";
                readonly properties: {
                    readonly src: {
                        readonly type: "string";
                    };
                    readonly srcSet: {
                        readonly type: "string";
                    };
                };
                readonly additionalProperties: false;
            };
        };
        readonly additionalProperties: false;
    };
};
export declare const entitiesCatalogConfigSchema: {
    readonly type: "object";
    readonly properties: {
        readonly show: {
            readonly type: "boolean";
            readonly default: false;
        };
        readonly entityTypes: {
            readonly type: "object";
            readonly additionalProperties: {
                readonly type: "object";
                readonly required: readonly ["name", "description", "metadataSchema"];
                readonly properties: {
                    readonly name: {
                        readonly type: "string";
                        readonly description: "Display name of the entity type";
                    };
                    readonly description: {
                        readonly type: "string";
                        readonly description: "Description of the entity type";
                    };
                    readonly metadataSchema: {
                        readonly type: "object";
                        readonly required: readonly ["type", "properties"];
                        readonly properties: {
                            readonly type: {
                                readonly type: "string";
                                readonly enum: readonly ["object"];
                            };
                            readonly description: {
                                readonly type: "string";
                            };
                            readonly properties: {
                                readonly type: "object";
                                readonly additionalProperties: {
                                    readonly type: "object";
                                    readonly properties: {
                                        readonly type: {
                                            readonly type: "string";
                                            readonly enum: readonly ["string", "number", "boolean", "array", "object"];
                                        };
                                        readonly description: {
                                            readonly type: "string";
                                        };
                                        readonly example: {
                                            readonly oneOf: readonly [{
                                                readonly type: "string";
                                            }, {
                                                readonly type: "number";
                                            }, {
                                                readonly type: "boolean";
                                            }, {
                                                readonly type: "array";
                                            }, {
                                                readonly type: "object";
                                            }];
                                        };
                                        readonly enum: {
                                            readonly type: "array";
                                            readonly items: {
                                                readonly type: "string";
                                            };
                                        };
                                        readonly pattern: {
                                            readonly type: "string";
                                        };
                                        readonly format: {
                                            readonly type: "string";
                                        };
                                        readonly minimum: {
                                            readonly type: "number";
                                        };
                                        readonly maximum: {
                                            readonly type: "number";
                                        };
                                        readonly items: {
                                            readonly type: "object";
                                        };
                                    };
                                    readonly additionalProperties: true;
                                };
                            };
                            readonly required: {
                                readonly type: "array";
                                readonly items: {
                                    readonly type: "string";
                                };
                            };
                            readonly additionalProperties: {
                                readonly type: "boolean";
                            };
                        };
                        readonly additionalProperties: true;
                    };
                    readonly icon: {
                        readonly type: "object";
                        readonly properties: {
                            readonly src: {
                                readonly type: "string";
                            };
                            readonly srcSet: {
                                readonly type: "string";
                            };
                        };
                        readonly additionalProperties: false;
                    };
                };
                readonly additionalProperties: false;
            };
        };
        readonly catalogs: {
            readonly type: "object";
            readonly properties: {
                readonly all: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly services: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly domains: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly teams: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly users: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly apiDescriptions: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly dataSchemas: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
                readonly apiOperations: {
                    readonly type: "object";
                    readonly properties: {
                        readonly slug: {
                            readonly type: "string";
                        };
                        readonly hide: {
                            readonly type: "boolean";
                        };
                        readonly includes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["type"];
                                readonly properties: {
                                    readonly type: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly excludes: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["key"];
                                readonly properties: {
                                    readonly key: {
                                        readonly type: "string";
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly filters: {
                            readonly type: "array";
                            readonly items: {
                                readonly type: "object";
                                readonly required: readonly ["property", "title"];
                                readonly properties: {
                                    readonly property: {
                                        readonly type: "string";
                                    };
                                    readonly hide: {
                                        readonly type: "boolean";
                                    };
                                    readonly label: {
                                        readonly type: "string";
                                    };
                                    readonly options: {
                                        readonly type: "array";
                                        readonly items: {
                                            readonly type: "string";
                                        };
                                    };
                                    readonly type: {
                                        readonly type: "string";
                                        readonly enum: readonly ["select", "checkboxes", "date-range"];
                                        readonly default: "checkboxes";
                                    };
                                    readonly title: {
                                        readonly type: "string";
                                    };
                                    readonly titleTranslationKey: {
                                        readonly type: "string";
                                    };
                                    readonly parentFilter: {
                                        readonly type: "string";
                                    };
                                    readonly valuesMapping: {
                                        readonly type: "object";
                                        readonly additionalProperties: {
                                            readonly type: "string";
                                        };
                                    };
                                };
                                readonly additionalProperties: false;
                            };
                        };
                        readonly titleTranslationKey: {
                            readonly type: "string";
                        };
                        readonly descriptionTranslationKey: {
                            readonly type: "string";
                        };
                        readonly catalogSwitcherLabelTranslationKey: {
                            readonly type: "string";
                        };
                    };
                    readonly additionalProperties: false;
                };
            };
            readonly additionalProperties: {
                readonly type: "object";
                readonly properties: {
                    readonly slug: {
                        readonly type: "string";
                    };
                    readonly hide: {
                        readonly type: "boolean";
                    };
                    readonly includes: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "object";
                            readonly required: readonly ["type"];
                            readonly properties: {
                                readonly type: {
                                    readonly type: "string";
                                };
                            };
                            readonly additionalProperties: false;
                        };
                    };
                    readonly excludes: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "object";
                            readonly required: readonly ["key"];
                            readonly properties: {
                                readonly key: {
                                    readonly type: "string";
                                };
                            };
                            readonly additionalProperties: false;
                        };
                    };
                    readonly filters: {
                        readonly type: "array";
                        readonly items: {
                            readonly type: "object";
                            readonly required: readonly ["property", "title"];
                            readonly properties: {
                                readonly property: {
                                    readonly type: "string";
                                };
                                readonly hide: {
                                    readonly type: "boolean";
                                };
                                readonly label: {
                                    readonly type: "string";
                                };
                                readonly options: {
                                    readonly type: "array";
                                    readonly items: {
                                        readonly type: "string";
                                    };
                                };
                                readonly type: {
                                    readonly type: "string";
                                    readonly enum: readonly ["select", "checkboxes", "date-range"];
                                    readonly default: "checkboxes";
                                };
                                readonly title: {
                                    readonly type: "string";
                                };
                                readonly titleTranslationKey: {
                                    readonly type: "string";
                                };
                                readonly parentFilter: {
                                    readonly type: "string";
                                };
                                readonly valuesMapping: {
                                    readonly type: "object";
                                    readonly additionalProperties: {
                                        readonly type: "string";
                                    };
                                };
                            };
                            readonly additionalProperties: false;
                        };
                    };
                    readonly titleTranslationKey: {
                        readonly type: "string";
                    };
                    readonly descriptionTranslationKey: {
                        readonly type: "string";
                    };
                    readonly catalogSwitcherLabelTranslationKey: {
                        readonly type: "string";
                    };
                };
                readonly additionalProperties: false;
            };
        };
    };
    readonly additionalProperties: false;
};
