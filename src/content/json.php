<?php
/**
 * json内容处理
 * Author:show
 */
namespace phpspiderman\content;

class json
{
    public static function decode($data)
    {
        return json_decode($data,true);
    }
}