<?php
/**
 * xml内容处理
 * Author:show
 */
namespace phpspiderman\content;

class xml
{
    public static function decode($data)
    {
        return simplexml_load_string($data);
    }
}