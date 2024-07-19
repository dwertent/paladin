/*
 * Copyright © 2024 Kaleido, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
 * the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
 * an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package io.kaleido.kata;

import paladin.kata.Kata;

public class SubmitTransactionRequest extends Request {

    private final String contractAddress;
    private final String from;
    private final String idempotencyKey;
    private final String payloadJSON;

    public SubmitTransactionRequest(
            Handler transactionHandler,
            ResponseHandler responseHandler,
            String contractAddress,
            String from,
            String idempotencyKey,
            String payloadJSON) {
        super(transactionHandler, responseHandler);
        this.contractAddress = contractAddress;
        this.from = from;
        this.idempotencyKey = idempotencyKey;
        this.payloadJSON = payloadJSON;
    }

    @Override
    public Kata.Request getRequestMessage() {
        return Kata.Request.newBuilder()
                .setType(Kata.REQUEST_TYPE.SUBMIT_TRANSACTION_REQUEST)
                .setSubmitTransactionRequest(Kata.SubmitTransactionRequest.newBuilder()
                        .setContractAddress(this.contractAddress)
                        .setFrom(this.from)
                        .setIdempotencyKey(this.idempotencyKey)
                        .setPayloadJSON(this.payloadJSON))
                .build();
    }

}