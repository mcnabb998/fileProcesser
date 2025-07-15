# Profile Field Glossary

| Field | Description |
|-------|-------------|
| `parserId` | Identifier for the plug-in parser to use (`csv_pipe`, `fixed_width`, or `xlsx_sheet`). |
| `maxBytes` | Maximum allowed file size in bytes. |
| `maxRows` | Maximum number of rows permitted in the file. |
| `rowStateMachineArn` | ARN of the Step Function that processes each row. |
| `mapMaxConcurrency` | Maximum parallelism for the Map state when invoking row processors. |
| `rowValidation` | Rules applied to each row before processing. |
| `rowValidation.required` | List of required field names. |
| `rowValidation.regex` | Map of field names to regex patterns for validation. |
| `preProcessors` | Optional list of pre-processing plug-ins. |
| `enrichments` | Optional list of enrichment plug-ins. |
| `targets` | Array of Salesforce object mappings to upsert. |
| `targets[].object` | Salesforce object API name. |
| `targets[].externalId` | External ID field for upsert operations. |
| `targets[].fieldMap` | Mapping from source column names to Salesforce fields. |
| `targets[].link` | Optional reference fields linking to previously created objects. |
| `targets[].postCreateRules` | Optional plug-ins run after record creation. |
