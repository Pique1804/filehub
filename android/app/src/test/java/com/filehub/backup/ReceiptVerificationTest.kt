package com.filehub.backup

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ReceiptVerificationTest {
    @Test fun failedRequestLeavesEveryReceiptPending() = runBlocking {
        val persisted=mutableListOf<Int>()
        val result=ReceiptVerification.run((1..25).toList(),{it.toString()},{null},{item,_->persisted.add(item)})
        assertEquals(25,result.pending); assertEquals(0,result.checked); assertTrue(persisted.isEmpty())
        val summary = ReceiptVerification.Summary(3, false, result.pending, 0)
        assertEquals(R.string.transfer_ui_summary_verification_pending, summary.verificationLabel)
    }

    @Test fun partialAndUnverifiedAnswersAreNotInvalidations() = runBlocking {
        val persisted=mutableMapOf<Int,Boolean>()
        val result=ReceiptVerification.run(listOf(1,2,3,4),{it.toString()}, {
            mapOf("1" to ReceiptCheck(true),"2" to ReceiptCheck(false),"3" to ReceiptCheck(false,false),"unrequested" to ReceiptCheck(true))
        },{item,valid->persisted[item]=valid})
        assertEquals(mapOf(1 to true,2 to false),persisted)
        assertEquals(2,result.pending); assertEquals(1,result.invalid)
    }

    @Test fun moreThanOneThousandContinuesFromDurableRemainder() = runBlocking {
        val pending=(1..1041).toMutableSet()
        val request: suspend (List<Int>)->Map<String,ReceiptCheck> = {batch->batch.associate{it.toString() to ReceiptCheck(true)}}
        val first=ReceiptVerification.run(pending.toList(),{it.toString()},request,{item,_->pending.remove(item)})
        assertEquals(1000,first.checked); assertEquals(41,first.pending)
        // A new worker reconstructs pending work from the persisted verification timestamps.
        val second=ReceiptVerification.run(pending.toList(),{it.toString()},request,{item,_->pending.remove(item)})
        assertEquals(41,second.checked); assertEquals(0,second.pending); assertTrue(pending.isEmpty())
    }

    @Test fun interruptionKeepsOnlyUncommittedKeysDue() = runBlocking {
        val pending=(1..45).toMutableSet()
        var calls=0
        try {
            ReceiptVerification.run(pending.toList(),{it.toString()},{batch->
                if(++calls==2) throw CancellationException("interrupted")
                batch.associate{it.toString() to ReceiptCheck(true)}
            },{item,_->pending.remove(item)})
            fail("Cancellation must propagate")
        } catch (_: CancellationException) { }
        assertEquals((21..45).toSet(),pending)
    }

    @Test fun recoveredVerificationDoesNotEraseUploadRetryStatus() {
        val summary = ReceiptVerification.Summary(3, true, 0, 2)
        assertEquals(R.string.transfer_ui_summary_with_pending, summary.uploadLabel)
        assertEquals(R.string.transfer_ui_summary_verification_invalid, summary.verificationLabel)
    }

    @Test fun parserSupportsOldServerAndExplicitVerificationErrors() {
        val result=Api.parseReconcile(JSONObject("""{"items":[{"key":"old","valid":true},{"key":"error","valid":false,"verified":false}]}"""))
        assertEquals(ReceiptCheck(true),result["old"])
        assertEquals(ReceiptCheck(false,false),result["error"])
    }

    @Test(expected=IllegalArgumentException::class) fun duplicateAnswersAreRejected() {
        Api.parseReconcile(JSONObject("""{"items":[{"key":"x","valid":true},{"key":"x","valid":false}]}"""))
    }

    @Test(expected=IllegalArgumentException::class) fun stringBooleansAreRejected() {
        Api.parseReconcile(JSONObject("""{"items":[{"key":"x","valid":"true"}]}"""))
    }
}
