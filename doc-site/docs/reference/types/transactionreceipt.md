---
title: TransactionReceipt
---
{% include-markdown "./_includes/transactionreceipt_description.md" %}

### Example

```json
{
    "id": "00000000-0000-0000-0000-000000000000"
}
```

### Field Descriptions

| Field Name | Description | Type |
|------------|-------------|------|
| `id` | Transaction ID | [`UUID`](simpletypes.md#uuid) |
| `success` | Transaction success status | `bool` |
| `transactionHash` | Transaction hash | [`Bytes32`](simpletypes.md#bytes32) |
| `blockNumber` | Block number | `int64` |
| `transactionIndex` | Transaction index | `int64` |
| `logIndex` | Log index | `int64` |
| `source` | Event source | [`EthAddress`](simpletypes.md#ethaddress) |
| `failureMessage` | Failure message - set if transaction reverted | `string` |
| `revertData` | Encoded revert data - if available | [`HexBytes`](simpletypes.md#hexbytes) |
| `contractAddress` | New contract address - to be used in the 'To' field for subsequent invoke transactions | [`EthAddress`](simpletypes.md#ethaddress) |
