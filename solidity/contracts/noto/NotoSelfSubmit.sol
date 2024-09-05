// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.20;

import {Noto} from "./Noto.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/**
 * Noto variant which allows _any_ address to submit a transfer, as long as
 * it is accompanied by an EIP-712 signature from the notary. The notary
 * signature is recovered and verified.
 */
contract NotoSelfSubmit is Noto {
    constructor(
        bytes32 transactionId,
        address domain,
        address notary,
        bytes memory data
    ) Noto(transactionId, domain, notary, data) {}

    function transfer(
        bytes32[] memory inputs,
        bytes32[] memory outputs,
        bytes memory signature,
        bytes memory data
    ) external override {
        bytes32 txhash = _buildTXHash(inputs, outputs, data);
        address signer = ECDSA.recover(txhash, signature);
        requireNotary(signer);
        _transfer(inputs, outputs, data);
    }

    function approve(
        address delegate,
        bytes32 txhash,
        bytes memory signature
    ) external override {
        address signer = ECDSA.recover(txhash, signature);
        requireNotary(signer);
        _approve(delegate, txhash);
    }
}