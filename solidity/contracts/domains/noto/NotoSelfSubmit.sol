// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.20;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {Noto} from "./Noto.sol";

/**
 * Noto variant which allows _any_ address to submit a transfer, as long as
 * it is accompanied by an EIP-712 signature from the notary. The notary
 * signature is recovered and verified.
 */
contract NotoSelfSubmit is Noto {
    bytes32 public constant NotoVariantSelfSubmit =
        0x0000000000000000000000000000000000000000000000000000000000000001;

    function initialize(
        address notary,
        bytes calldata config
    ) public override initializer returns (bytes memory) {
        __EIP712_init("noto", "0.0.1");

        NotoConfig_V0 memory configOut = _decodeConfig(config);
        configOut.notaryAddress = notary;
        configOut.variant = NotoVariantSelfSubmit;

        _notary = notary;
        return _encodeConfig(configOut);
    }

    function transfer(
        bytes32[] calldata inputs,
        bytes32[] calldata outputs,
        bytes calldata signature,
        bytes calldata data
    ) external override {
        bytes32 txhash = _buildTXHash(inputs, outputs, data);
        address signer = ECDSA.recover(txhash, signature);
        requireNotary(signer);
        _transfer(inputs, outputs, signature, data);
    }

    function approve(
        address delegate,
        bytes32 txhash,
        bytes calldata signature
    ) external override {
        address signer = ECDSA.recover(txhash, signature);
        requireNotary(signer);
        _approve(delegate, txhash, signature);
    }
}