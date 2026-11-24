package com.filehub.backup

import java.time.Instant
import java.time.LocalDate
import java.time.YearMonth
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Date headers and global positions only; photo objects live in bounded Paging pages. */
class PhotoTimeline(val sections:List<Section> = emptyList(),val photoCount:Int=0) {
    data class Section(val key:String,val label:String,val photoStart:Int,val count:Int,val itemStart:Int)
    val itemCount=photoCount+sections.size
    val headerPositions=sections.map {it.itemStart to it.label}
    fun sectionAt(item:Int):Section? {
        if(item !in 0 until itemCount)return null
        var low=0;var high=sections.lastIndex
        while(low<high){val mid=(low+high+1)/2;if(sections[mid].itemStart<=item)low=mid else high=mid-1}
        return sections[low]
    }
    fun headerAt(item:Int)=sectionAt(item)?.takeIf {it.itemStart==item}
    fun photoOffset(item:Int):Int {
        if(item>=itemCount)return photoCount
        val section=sectionAt(item) ?: return 0
        return section.photoStart+(item-section.itemStart-1).coerceAtLeast(0)
    }
    companion object {
        fun build(times:Sequence<Long>,months:Boolean,zone:ZoneId=ZoneId.systemDefault(),today:LocalDate=LocalDate.now(zone)):PhotoTimeline {
            val day=DateTimeFormatter.ofPattern("d MMMM yyyy",Locale("ru"))
            val month=DateTimeFormatter.ofPattern("LLLL yyyy",Locale("ru"))
            val out=ArrayList<Section>();var total=0
            for(time in times) {
                val date=Instant.ofEpochSecond(time).atZone(zone).toLocalDate()
                val key=if(months)YearMonth.from(date).toString() else date.toString()
                val last=out.lastOrNull()
                if(last?.key==key)out[out.lastIndex]=last.copy(count=last.count+1)
                else {
                    val label=when {months->date.format(month).replaceFirstChar {it.uppercase()};date==today->"Сегодня";date==today.minusDays(1)->"Вчера";else->date.format(day)}
                    out.add(Section(key,label,total,1,total+out.size))
                }
                total++
            }
            return PhotoTimeline(out,total)
        }
    }
}
